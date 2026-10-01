package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/myahse/memory/backend/internal/db"
	"github.com/myahse/memory/backend/internal/extract"
	"github.com/myahse/memory/backend/internal/memory"
	"github.com/myahse/memory/backend/internal/storage"
)

var (
	imageMIMEs = map[string]bool{"image/jpeg": true, "image/png": true, "image/webp": true, "image/gif": true}
	audioMIMEs = map[string]bool{
		"audio/mp4": true, "audio/m4a": true, "audio/x-m4a": true, "audio/aac": true, "audio/mpeg": true,
		"audio/mp3": true, "audio/wav": true, "audio/x-wav": true, "audio/webm": true, "audio/ogg": true, "audio/flac": true,
	}
	uuidRe = regexp.MustCompile(`^[0-9a-fA-F-]{36}$`)
	hashRe = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

const maxNoteRunes = 100_000

type createRequest struct {
	Type        string         `json:"type"`
	Title       string         `json:"title"`
	Content     string         `json:"content"`
	SourceURL   string         `json:"source_url"`
	MimeType    string         `json:"mime_type"`
	FileSize    int64          `json:"file_size"`
	ContentHash string         `json:"content_hash"`
	ClientID    string         `json:"client_id"`
	CapturedAt  *time.Time     `json:"captured_at"`
	Metadata    map[string]any `json:"metadata"`
	// Duplicate handling: "" / "ask" → 409 with candidates; "keep_both"; "replace" (+ replace_id).
	OnDuplicate string `json:"on_duplicate"`
	ReplaceID   string `json:"replace_id"`
}

type uploadTarget struct {
	URL    string `json:"url"`
	Method string `json:"method"`
	Path   string `json:"path"`
}

type createResponse struct {
	Memory *memory.Memory `json:"memory"`
	Upload *uploadTarget  `json:"upload,omitempty"`
}

// validateFile normalises type/MIME combinations for uploaded files.
func validateFile(typ, mime string) (string, error) {
	mime = strings.ToLower(strings.TrimSpace(strings.Split(mime, ";")[0]))
	switch typ {
	case "photo", "screenshot", "receipt":
		if mime == "application/pdf" && typ == "receipt" {
			return "pdf", nil // a PDF receipt is processed as a document
		}
		if !imageMIMEs[mime] {
			if mime == "image/heic" || mime == "image/heif" {
				return "", fmt.Errorf("HEIC images must be converted to JPEG before upload")
			}
			return "", fmt.Errorf("unsupported image type %q (use JPEG, PNG, WebP or GIF)", mime)
		}
	case "pdf":
		if mime != "application/pdf" {
			return "", fmt.Errorf("expected a PDF file")
		}
	case "document":
		if mime == "application/pdf" {
			return "pdf", nil
		}
		if !extract.DocumentMIMEs[mime] {
			return "", fmt.Errorf("unsupported document type %q (use PDF, DOCX, TXT, Markdown, CSV, HTML or RTF)", mime)
		}
	case "voice":
		if !audioMIMEs[mime] {
			return "", fmt.Errorf("unsupported audio type %q", mime)
		}
	}
	return typ, nil
}

func (s *Server) createMemory(w http.ResponseWriter, r *http.Request) {
	u := user(r)
	var req createRequest
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if !memory.ValidType(req.Type) {
		writeError(w, http.StatusBadRequest, "bad_request", "Unknown memory type.")
		return
	}
	if req.ClientID != "" && (len(req.ClientID) > 100) {
		writeError(w, http.StatusBadRequest, "bad_request", "client_id is too long.")
		return
	}
	req.ContentHash = strings.ToLower(req.ContentHash)
	if req.ContentHash != "" && !hashRe.MatchString(req.ContentHash) {
		writeError(w, http.StatusBadRequest, "bad_request", "content_hash must be a hex SHA-256.")
		return
	}
	if utf8.RuneCountInString(req.Title) > 300 {
		writeError(w, http.StatusBadRequest, "bad_request", "Title is too long.")
		return
	}
	if req.Metadata != nil {
		if b, _ := json.Marshal(req.Metadata); len(b) > 16<<10 {
			writeError(w, http.StatusBadRequest, "bad_request", "Metadata is too large.")
			return
		}
	}

	isFile := memory.IsFileType(req.Type)
	switch {
	case isFile:
		typ, err := validateFile(req.Type, req.MimeType)
		if err != nil {
			writeError(w, http.StatusUnsupportedMediaType, "unsupported_file", err.Error())
			return
		}
		req.Type = typ
		req.MimeType = strings.ToLower(strings.TrimSpace(strings.Split(req.MimeType, ";")[0]))
		if req.FileSize <= 0 {
			writeError(w, http.StatusBadRequest, "bad_request", "file_size is required.")
			return
		}
	case req.Type == "note":
		req.Content = strings.TrimSpace(req.Content)
		if req.Content == "" {
			writeError(w, http.StatusBadRequest, "bad_request", "Write something first.")
			return
		}
		if utf8.RuneCountInString(req.Content) > maxNoteRunes {
			writeError(w, http.StatusRequestEntityTooLarge, "too_large", "This note is too long.")
			return
		}
		if req.ContentHash == "" {
			req.ContentHash = sha256Hex(strings.ToLower(req.Content))
		}
	case req.Type == "link":
		lu, err := extract.ValidateURL(req.SourceURL)
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		req.SourceURL = lu.String()
		if req.ContentHash == "" {
			req.ContentHash = sha256Hex(strings.TrimRight(lu.String(), "/"))
		}
	}

	ctx := r.Context()

	// Idempotent retries from offline clients.
	if req.ClientID != "" {
		var existing *memory.Memory
		err := s.DB.WithUser(ctx, u.ID, func(tx pgx.Tx) error {
			var err error
			existing, err = memory.FindByClientID(ctx, tx, u.ID, req.ClientID)
			return err
		})
		if err == nil {
			resp := createResponse{Memory: existing}
			if isFile && existing.FilePath == "" {
				if resp.Upload, err = s.uploadTarget(ctx, u.ID, existing.ID); err != nil {
					s.fail(w, r, err)
					return
				}
			}
			writeJSON(w, http.StatusOK, resp)
			return
		} else if !db.IsNoRows(err) {
			s.fail(w, r, err)
			return
		}
	}

	if isFile {
		if err := s.Quota.CheckUpload(ctx, u.ID, req.FileSize); err != nil {
			s.fail(w, r, err)
			return
		}
	} else if err := s.Quota.CheckUpload(ctx, u.ID, 0); err != nil {
		s.fail(w, r, err)
		return
	}

	// Duplicate detection: never delete automatically, ask the user.
	if req.ContentHash != "" && req.OnDuplicate != "keep_both" {
		var dups []*memory.Memory
		err := s.DB.WithUser(ctx, u.ID, func(tx pgx.Tx) error {
			var err error
			dups, err = memory.FindDuplicates(ctx, tx, u.ID, req.ContentHash)
			return err
		})
		if err != nil {
			s.fail(w, r, err)
			return
		}
		if len(dups) > 0 && req.OnDuplicate != "replace" {
			s.sign(ctx, dups, false)
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":      apiError{Code: "duplicate", Message: "This looks similar to an existing memory."},
				"duplicates": dups,
			})
			return
		}
	}
	if req.OnDuplicate == "replace" {
		if !uuidRe.MatchString(req.ReplaceID) {
			writeError(w, http.StatusBadRequest, "bad_request", "replace_id is required to replace a memory.")
			return
		}
		if req.Metadata == nil {
			req.Metadata = map[string]any{}
		}
		req.Metadata["replaces"] = req.ReplaceID
	}

	var m *memory.Memory
	err := s.DB.WithUser(ctx, u.ID, func(tx pgx.Tx) error {
		var err error
		m, err = memory.Insert(ctx, tx, memory.NewMemory{
			UserID: u.ID, Type: req.Type, Title: strings.TrimSpace(req.Title),
			Content: req.Content, SourceURL: req.SourceURL, MimeType: req.MimeType, FileSize: req.FileSize,
			ContentHash: req.ContentHash, ClientID: req.ClientID, CapturedAt: req.CapturedAt, Metadata: req.Metadata,
		})
		if err != nil {
			return err
		}
		if req.Title != "" {
			if _, err := tx.Exec(ctx, `update memories set user_edited = array['title'] where id = $1`, m.ID); err != nil {
				return err
			}
		}
		return nil
	})
	if err == nil && !isFile {
		err = memory.EnqueueJob(ctx, s.DB.Pool, u.ID, m.ID, "process", "")
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !isFile && req.OnDuplicate == "replace" {
		s.deleteMemoryAndFiles(ctx, u.ID, req.ReplaceID)
	}

	resp := createResponse{Memory: m}
	if isFile {
		if resp.Upload, err = s.uploadTarget(ctx, u.ID, m.ID); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (s *Server) uploadTarget(ctx context.Context, userID, memoryID string) (*uploadTarget, error) {
	path := storage.OriginalPath(userID, memoryID)
	url, err := s.Store.SignedUploadURL(ctx, path)
	if err != nil {
		return nil, err
	}
	return &uploadTarget{URL: url, Method: "PUT", Path: path}, nil
}

// uploadURL issues a fresh upload URL (e.g. after an interrupted upload).
func (s *Server) uploadURL(w http.ResponseWriter, r *http.Request) {
	u := user(r)
	id := r.PathValue("id")
	m, err := s.loadMemory(r.Context(), u.ID, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !memory.IsFileType(m.Type) {
		writeError(w, http.StatusBadRequest, "bad_request", "This memory has no file.")
		return
	}
	t, err := s.uploadTarget(r.Context(), u.ID, m.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

// uploadComplete verifies the object exists and starts processing.
func (s *Server) uploadComplete(w http.ResponseWriter, r *http.Request) {
	u := user(r)
	ctx := r.Context()
	m, err := s.loadMemory(ctx, u.ID, r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !memory.IsFileType(m.Type) {
		writeError(w, http.StatusBadRequest, "bad_request", "This memory has no file.")
		return
	}
	path := storage.OriginalPath(u.ID, m.ID)
	size, _, err := s.Store.Stat(ctx, path)
	if errors.Is(err, storage.ErrNotFound) {
		writeError(w, http.StatusConflict, "upload_missing", "The upload did not finish. Please try again.")
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if size <= 0 {
		size = m.FileSize
	}
	usage, err := s.Quota.Usage(ctx, u.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if size > usage.MaxFileBytes || usage.StorageUsed-m.FileSize+size > usage.StorageLimit {
		_ = s.Store.Delete(ctx, []string{path})
		if size > usage.MaxFileBytes {
			writeError(w, http.StatusRequestEntityTooLarge, "file_too_large", "This file is larger than your plan allows.")
		} else {
			writeError(w, http.StatusInsufficientStorage, "insufficient_storage", "Not enough storage left on your plan for this file.")
		}
		return
	}

	alreadyQueued := m.FilePath != "" && m.Status != "failed"
	err = s.DB.WithService(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `update memories set file_path = $3, file_size = $4 where id = $1 and user_id = $2`,
			m.ID, u.ID, path, size); err != nil {
			return err
		}
		if alreadyQueued {
			return nil
		}
		return memory.EnqueueJob(ctx, tx, u.ID, m.ID, "process", "pending")
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var meta map[string]any
	_ = json.Unmarshal(m.Metadata, &meta)
	if rid, ok := meta["replaces"].(string); ok && rid != m.ID && uuidRe.MatchString(rid) {
		s.deleteMemoryAndFiles(ctx, u.ID, rid)
	}
	m, err = s.loadMemory(ctx, u.ID, m.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"memory": m})
}

func (s *Server) checkDuplicate(w http.ResponseWriter, r *http.Request) {
	u := user(r)
	var req struct {
		ContentHash string `json:"content_hash"`
	}
	if err := decode(r, &req); err != nil || !hashRe.MatchString(strings.ToLower(req.ContentHash)) {
		writeError(w, http.StatusBadRequest, "bad_request", "content_hash must be a hex SHA-256.")
		return
	}
	var dups []*memory.Memory
	err := s.DB.WithUser(r.Context(), u.ID, func(tx pgx.Tx) error {
		var err error
		dups, err = memory.FindDuplicates(r.Context(), tx, u.ID, strings.ToLower(req.ContentHash))
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.sign(r.Context(), dups, false)
	if dups == nil {
		dups = []*memory.Memory{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"duplicates": dups})
}

func (s *Server) loadMemory(ctx context.Context, userID, id string) (*memory.Memory, error) {
	if !uuidRe.MatchString(id) {
		return nil, pgx.ErrNoRows
	}
	var m *memory.Memory
	err := s.DB.WithUser(ctx, userID, func(tx pgx.Tx) error {
		var err error
		m, err = memory.Get(ctx, tx, id)
		return err
	})
	return m, err
}

func parseListParams(r *http.Request) (memory.ListParams, error) {
	q := r.URL.Query()
	p := memory.ListParams{Category: q.Get("category"), Tag: q.Get("tag"), Status: q.Get("status")}
	if t := q.Get("type"); t != "" {
		for _, x := range strings.Split(t, ",") {
			if !memory.ValidType(x) {
				return p, fmt.Errorf("unknown type %q", x)
			}
			p.Types = append(p.Types, x)
		}
	}
	for key, dst := range map[string]**time.Time{"from": &p.From, "to": &p.To, "before": &p.Before} {
		if v := q.Get(key); v != "" {
			t, err := time.Parse(time.RFC3339Nano, v)
			if err != nil {
				return p, fmt.Errorf("%s must be an RFC 3339 timestamp", key)
			}
			*dst = &t
		}
	}
	if l, err := strconv.Atoi(q.Get("limit")); err == nil {
		p.Limit = l
	}
	return p, nil
}

func (s *Server) listMemories(w http.ResponseWriter, r *http.Request) {
	u := user(r)
	p, err := parseListParams(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	var ms []*memory.Memory
	err = s.DB.WithUser(r.Context(), u.ID, func(tx pgx.Tx) error {
		var err error
		ms, err = memory.List(r.Context(), tx, p)
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	for _, m := range ms {
		m.Content = preview(m.Content)
	}
	s.sign(r.Context(), ms, false)
	var next string
	if limit := p.Limit; len(ms) > 0 && (limit <= 0 && len(ms) == 30 || len(ms) == limit) {
		next = ms[len(ms)-1].CreatedAt.Format(time.RFC3339Nano)
	}
	writeJSON(w, http.StatusOK, map[string]any{"memories": ms, "next_cursor": next})
}

func preview(s string) string {
	r := []rune(s)
	if len(r) > 280 {
		return string(r[:280]) + "…"
	}
	return s
}

func (s *Server) getMemory(w http.ResponseWriter, r *http.Request) {
	u := user(r)
	m, err := s.loadMemory(r.Context(), u.ID, r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.sign(r.Context(), []*memory.Memory{m}, true)
	writeJSON(w, http.StatusOK, map[string]any{"memory": m})
}

func (s *Server) downloadMemory(w http.ResponseWriter, r *http.Request) {
	u := user(r)
	m, err := s.loadMemory(r.Context(), u.ID, r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if m.FilePath == "" {
		writeError(w, http.StatusNotFound, "not_found", "This memory has no file.")
		return
	}
	url, err := s.Store.SignedURL(r.Context(), m.FilePath, 10*time.Minute, downloadName(m))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": url})
}

func downloadName(m *memory.Memory) string {
	name := strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r < 32 {
			return '-'
		}
		return r
	}, m.Title)
	if name == "" {
		name = "memory"
	}
	ext := map[string]string{
		"image/jpeg": ".jpg", "image/png": ".png", "image/webp": ".webp", "image/gif": ".gif",
		"application/pdf": ".pdf", "text/plain": ".txt", "text/markdown": ".md", "text/csv": ".csv",
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document": ".docx",
		"audio/mp4": ".m4a", "audio/m4a": ".m4a", "audio/x-m4a": ".m4a", "audio/mpeg": ".mp3", "audio/webm": ".webm",
		"audio/wav": ".wav", "audio/ogg": ".ogg", "audio/aac": ".aac",
	}[m.MimeType]
	return name + ext
}

func (s *Server) patchMemory(w http.ResponseWriter, r *http.Request) {
	u := user(r)
	var p memory.Patch
	if err := decode(r, &p); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if p.Type != nil && !memory.ValidType(*p.Type) {
		writeError(w, http.StatusBadRequest, "bad_request", "Unknown memory type.")
		return
	}
	if p.Title != nil && utf8.RuneCountInString(*p.Title) > 300 {
		writeError(w, http.StatusBadRequest, "bad_request", "Title is too long.")
		return
	}
	if p.Tags != nil && len(*p.Tags) > 30 {
		writeError(w, http.StatusBadRequest, "bad_request", "Too many tags.")
		return
	}
	if p.Content != nil && utf8.RuneCountInString(*p.Content) > 200_000 {
		writeError(w, http.StatusBadRequest, "bad_request", "Content is too long.")
		return
	}
	ctx := r.Context()
	m, err := s.loadMemory(ctx, u.ID, r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	reindex := p.Title != nil || p.Summary != nil || p.Content != nil || p.Tags != nil || p.Category != nil
	err = s.DB.WithUser(ctx, u.ID, func(tx pgx.Tx) error {
		return memory.Update(ctx, tx, u.ID, m.ID, p)
	})
	if err == nil && reindex && m.Status == "ready" {
		err = memory.EnqueueJob(ctx, s.DB.Pool, u.ID, m.ID, "reembed", "")
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	m, err = s.loadMemory(ctx, u.ID, m.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.sign(ctx, []*memory.Memory{m}, true)
	writeJSON(w, http.StatusOK, map[string]any{"memory": m})
}

func (s *Server) deleteMemory(w http.ResponseWriter, r *http.Request) {
	u := user(r)
	id := r.PathValue("id")
	if _, err := s.loadMemory(r.Context(), u.ID, id); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.deleteMemoryAndFiles(r.Context(), u.ID, id); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// deleteMemoryAndFiles removes the row (cascading chunks, tags, jobs, shares)
// and then every stored object for it.
func (s *Server) deleteMemoryAndFiles(ctx context.Context, userID, id string) error {
	var deleted bool
	err := s.DB.WithUser(ctx, userID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `delete from memories where id = $1`, id)
		if err != nil {
			return err
		}
		deleted = tag.RowsAffected() > 0
		if deleted {
			return memory.DeleteOrphanTags(ctx, tx, userID)
		}
		return nil
	})
	if err != nil || !deleted {
		return err
	}
	paths := []string{storage.OriginalPath(userID, id), storage.ThumbnailPath(userID, id)}
	if err := s.Store.Delete(context.WithoutCancel(ctx), paths); err != nil {
		s.Log.Error("delete memory files", "memory", id, "err", err)
	}
	return nil
}

func (s *Server) retryMemory(w http.ResponseWriter, r *http.Request) {
	u := user(r)
	ctx := r.Context()
	m, err := s.loadMemory(ctx, u.ID, r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if m.Status != "failed" {
		writeError(w, http.StatusConflict, "not_failed", "This memory is not in a failed state.")
		return
	}
	if memory.IsFileType(m.Type) && m.FilePath == "" {
		writeError(w, http.StatusConflict, "upload_missing", "The file was never uploaded. Please upload it again.")
		return
	}
	err = s.DB.WithService(ctx, func(tx pgx.Tx) error {
		return memory.EnqueueJob(ctx, tx, u.ID, m.ID, "process", "pending")
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	m.Status, m.ProcessingError = "pending", ""
	writeJSON(w, http.StatusAccepted, map[string]any{"memory": m})
}

func (s *Server) relatedMemories(w http.ResponseWriter, r *http.Request) {
	u := user(r)
	id := r.PathValue("id")
	if !uuidRe.MatchString(id) {
		writeError(w, http.StatusNotFound, "not_found", "Not found.")
		return
	}
	type item struct {
		Memory     *memory.Memory `json:"memory"`
		Similarity float64        `json:"similarity"`
	}
	out := []item{}
	err := s.DB.WithUser(r.Context(), u.ID, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `select id, similarity from related_memories($1, 6)`, id)
		if err != nil {
			return err
		}
		var ids []string
		sims := map[string]float64{}
		for rows.Next() {
			var rid string
			var sim float64
			if err := rows.Scan(&rid, &sim); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, rid)
			sims[rid] = sim
		}
		rows.Close()
		ms, err := memory.GetMany(r.Context(), tx, ids)
		if err != nil {
			return err
		}
		for _, m := range ms {
			m.Content = preview(m.Content)
			out = append(out, item{Memory: m, Similarity: sims[m.ID]})
		}
		return nil
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	ms := make([]*memory.Memory, len(out))
	for i := range out {
		ms[i] = out[i].Memory
	}
	s.sign(r.Context(), ms, false)
	writeJSON(w, http.StatusOK, map[string]any{"related": out})
}

// timeline groups memories by month, newest first.
func (s *Server) timeline(w http.ResponseWriter, r *http.Request) {
	u := user(r)
	p, err := parseListParams(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if p.Limit <= 0 {
		p.Limit = 60
	}
	var ms []*memory.Memory
	err = s.DB.WithUser(r.Context(), u.ID, func(tx pgx.Tx) error {
		var err error
		ms, err = memory.List(r.Context(), tx, p)
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.sign(r.Context(), ms, false)
	type group struct {
		Month    string           `json:"month"`
		Label    string           `json:"label"`
		Memories []*memory.Memory `json:"memories"`
	}
	groups := []*group{}
	for _, m := range ms {
		m.Content = preview(m.Content)
		key := m.CreatedAt.UTC().Format("2006-01")
		if len(groups) == 0 || groups[len(groups)-1].Month != key {
			groups = append(groups, &group{Month: key, Label: m.CreatedAt.UTC().Format("January 2006")})
		}
		g := groups[len(groups)-1]
		g.Memories = append(g.Memories, m)
	}
	var next string
	if len(ms) == p.Limit {
		next = ms[len(ms)-1].CreatedAt.Format(time.RFC3339Nano)
	}
	writeJSON(w, http.StatusOK, map[string]any{"groups": groups, "next_cursor": next})
}

// facets returns counts used to build filter chips.
func (s *Server) facets(w http.ResponseWriter, r *http.Request) {
	u := user(r)
	type count struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}
	res := map[string][]count{"types": {}, "categories": {}, "tags": {}}
	queries := map[string]string{
		"types":      `select type::text, count(*) from memories group by 1 order by 2 desc`,
		"categories": `select category, count(*) from memories where category is not null group by 1 order by 2 desc limit 30`,
		"tags":       `select t.name, count(*) from memory_tags mt join tags t on t.id = mt.tag_id group by 1 order by 2 desc, 1 limit 50`,
	}
	err := s.DB.WithUser(r.Context(), u.ID, func(tx pgx.Tx) error {
		for key, q := range queries {
			rows, err := tx.Query(r.Context(), q)
			if err != nil {
				return err
			}
			for rows.Next() {
				var c count
				if err := rows.Scan(&c.Name, &c.Count); err != nil {
					rows.Close()
					return err
				}
				res[key] = append(res[key], c)
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}
