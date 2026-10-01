// Package api exposes Memory's HTTP API.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/myahse/memory/backend/internal/ai"
	"github.com/myahse/memory/backend/internal/auth"
	"github.com/myahse/memory/backend/internal/config"
	"github.com/myahse/memory/backend/internal/db"
	"github.com/myahse/memory/backend/internal/memory"
	"github.com/myahse/memory/backend/internal/quota"
	"github.com/myahse/memory/backend/internal/rag"
	"github.com/myahse/memory/backend/internal/search"
	"github.com/myahse/memory/backend/internal/storage"
)

type Server struct {
	Cfg      *config.Config
	DB       *db.DB
	Store    *storage.Client
	AI       *ai.Service
	Auth     *auth.Verifier
	Search   *search.Searcher
	RAG      *rag.Engine
	Quota    *quota.Service
	Log      *slog.Logger
	limiters sync.Map // key → *bucket
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := s.DB.Pool.Ping(ctx); err != nil {
			writeError(w, http.StatusServiceUnavailable, "unavailable", "database unreachable")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	// Public: a single shared memory, by unguessable token.
	mux.HandleFunc("GET /public/shares/{token}", s.limit("share", 30, s.getPublicShare))

	a := func(pattern string, h http.HandlerFunc) { mux.Handle(pattern, s.authed(h)) }

	a("GET /v1/me", s.getMe)
	a("PATCH /v1/me", s.patchMe)
	a("DELETE /v1/me/data", s.deleteMyData)

	a("POST /v1/memories", s.limit("create", 120, s.createMemory))
	a("GET /v1/memories", s.listMemories)
	a("GET /v1/memories/{id}", s.getMemory)
	a("PATCH /v1/memories/{id}", s.patchMemory)
	a("DELETE /v1/memories/{id}", s.deleteMemory)
	a("POST /v1/memories/{id}/upload-complete", s.uploadComplete)
	a("POST /v1/memories/{id}/upload-url", s.uploadURL)
	a("POST /v1/memories/{id}/retry", s.retryMemory)
	a("GET /v1/memories/{id}/related", s.relatedMemories)
	a("GET /v1/memories/{id}/download", s.downloadMemory)
	a("POST /v1/memories/check-duplicate", s.checkDuplicate)

	a("POST /v1/memories/{id}/shares", s.createShare)
	a("GET /v1/memories/{id}/shares", s.listShares)
	a("DELETE /v1/shares/{id}", s.revokeShare)

	a("GET /v1/timeline", s.timeline)
	a("GET /v1/facets", s.facets)
	a("POST /v1/search", s.limit("search", 60, s.search))
	a("POST /v1/ask", s.limit("ask", 20, s.ask))
	a("GET /v1/conversations", s.listConversations)
	a("GET /v1/conversations/{id}", s.getConversation)
	a("DELETE /v1/conversations/{id}", s.deleteConversation)

	return s.recoverer(s.logger(s.cors(mux)))
}

// ---------------------------------------------------------------------------
// Middleware
// ---------------------------------------------------------------------------

func (s *Server) authed(h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hdr := r.Header.Get("Authorization")
		token, ok := strings.CutPrefix(hdr, "Bearer ")
		if !ok || token == "" {
			writeError(w, http.StatusUnauthorized, "unauthenticated", "Sign in to continue.")
			return
		}
		u, err := s.Auth.Verify(r.Context(), token)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthenticated", "Your session has expired. Please sign in again.")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // JSON bodies only; files go straight to storage
		h(w, r.WithContext(auth.WithUser(r.Context(), u)))
	})
}

func (s *Server) cors(next http.Handler) http.Handler {
	allowed := map[string]bool{}
	for _, o := range s.Cfg.CORSOrigins {
		allowed[o] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && (allowed[origin] || allowed["*"]) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Max-Age", "600")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (s *Server) logger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(sw, r)
		if r.URL.Path == "/healthz" {
			return
		}
		// Never log query strings or bodies: they may contain personal data.
		s.Log.Info("http", "method", r.Method, "path", r.URL.Path, "status", sw.status,
			"took", time.Since(start).Round(time.Millisecond))
	})
}

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				s.Log.Error("panic", "err", v, "stack", string(debug.Stack()))
				writeError(w, http.StatusInternalServerError, "internal", "Something went wrong.")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// Token-bucket rate limiting per user (or IP for public routes).
type bucket struct {
	mu     sync.Mutex
	tokens float64
	last   time.Time
}

func (s *Server) limit(name string, perMinute int, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := name + ":"
		if u := auth.FromContext(r.Context()); u != nil {
			key += u.ID
		} else {
			key += clientIP(r)
		}
		v, _ := s.limiters.LoadOrStore(key, &bucket{tokens: float64(perMinute), last: time.Now()})
		b := v.(*bucket)
		b.mu.Lock()
		now := time.Now()
		b.tokens = min(float64(perMinute), b.tokens+now.Sub(b.last).Minutes()*float64(perMinute))
		b.last = now
		ok := b.tokens >= 1
		if ok {
			b.tokens--
		}
		b.mu.Unlock()
		if !ok {
			w.Header().Set("Retry-After", "10")
			writeError(w, http.StatusTooManyRequests, "rate_limited", "Too many requests. Please slow down.")
			return
		}
		h(w, r)
	}
}

func clientIP(r *http.Request) string {
	if f := r.Header.Get("X-Forwarded-For"); f != "" {
		return strings.TrimSpace(strings.Split(f, ",")[0])
	}
	return r.RemoteAddr
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{"error": apiError{Code: code, Message: msg}})
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("invalid request body: %w", err)
	}
	return nil
}

func user(r *http.Request) *auth.User { return auth.FromContext(r.Context()) }

// fail maps internal errors to safe responses.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case db.IsNoRows(err):
		writeError(w, http.StatusNotFound, "not_found", "Not found.")
	case errors.Is(err, quota.ErrAIQueries), errors.Is(err, quota.ErrProcessing):
		writeError(w, http.StatusPaymentRequired, "quota_exceeded", err.Error()+" Upgrade to Pro for more.")
	case errors.Is(err, quota.ErrStorage):
		writeError(w, http.StatusInsufficientStorage, "insufficient_storage", err.Error())
	case errors.Is(err, quota.ErrFileTooBig):
		writeError(w, http.StatusRequestEntityTooLarge, "file_too_large", err.Error())
	case errors.Is(err, context.DeadlineExceeded):
		writeError(w, http.StatusGatewayTimeout, "timeout", "The request took too long. Please try again.")
	default:
		var ae *ai.Error
		if errors.As(err, &ae) {
			s.Log.Error("ai error", "path", r.URL.Path, "err", err)
			writeError(w, http.StatusBadGateway, "ai_unavailable", "The AI service is unavailable right now. Please try again.")
			return
		}
		s.Log.Error("request failed", "path", r.URL.Path, "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "Something went wrong.")
	}
}

// sign fills thumbnail URLs (and file URLs when withFile) using short-lived signatures.
func (s *Server) sign(ctx context.Context, ms []*memory.Memory, withFile bool) {
	var paths []string
	for _, m := range ms {
		if m.ThumbnailPath != "" {
			paths = append(paths, m.ThumbnailPath)
		}
		if withFile && m.FilePath != "" {
			paths = append(paths, m.FilePath)
		}
	}
	if len(paths) == 0 {
		return
	}
	urls, err := s.Store.SignedURLs(ctx, paths, time.Hour)
	if err != nil {
		s.Log.Warn("sign urls", "err", err)
		return
	}
	for _, m := range ms {
		m.ThumbnailURL = urls[m.ThumbnailPath]
		if withFile {
			m.FileURL = urls[m.FilePath]
		}
	}
}
