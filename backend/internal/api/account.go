package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/myahse/memory/backend/internal/db"
	"github.com/myahse/memory/backend/internal/memory"
	"github.com/myahse/memory/backend/internal/storage"
)

func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

type profile struct {
	Name      string    `json:"name"`
	Email     string    `json:"email"`
	AvatarURL string    `json:"avatar_url"`
	Plan      string    `json:"plan"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *Server) loadProfile(ctx context.Context, userID, email, name string) (*profile, error) {
	p := &profile{Email: email}
	err := s.DB.WithUser(ctx, userID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `select coalesce(name,''), coalesce(avatar_url,''), plan::text, created_at from profiles where user_id = $1`, userID).
			Scan(&p.Name, &p.AvatarURL, &p.Plan, &p.CreatedAt)
	})
	if db.IsNoRows(err) {
		// The auth trigger normally creates the profile; recover if it didn't.
		_, err = s.DB.Pool.Exec(ctx, `insert into profiles (user_id, name) values ($1, nullif($2,'')) on conflict (user_id) do nothing`, userID, name)
		if err != nil {
			return nil, err
		}
		return s.loadProfile(ctx, userID, email, name)
	}
	return p, err
}

func (s *Server) getMe(w http.ResponseWriter, r *http.Request) {
	u := user(r)
	p, err := s.loadProfile(r.Context(), u.ID, u.Email, u.Name)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	usage, err := s.Quota.Usage(r.Context(), u.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": u.ID, "profile": p, "usage": usage})
}

func (s *Server) patchMe(w http.ResponseWriter, r *http.Request) {
	u := user(r)
	var req struct {
		Name      *string `json:"name"`
		AvatarURL *string `json:"avatar_url"`
	}
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if req.Name != nil && utf8.RuneCountInString(*req.Name) > 100 {
		writeError(w, http.StatusBadRequest, "bad_request", "Name is too long.")
		return
	}
	if req.AvatarURL != nil && *req.AvatarURL != "" && !strings.HasPrefix(*req.AvatarURL, "https://") {
		writeError(w, http.StatusBadRequest, "bad_request", "Avatar must be an https URL.")
		return
	}
	err := s.DB.WithUser(r.Context(), u.ID, func(tx pgx.Tx) error {
		_, err := tx.Exec(r.Context(), `update profiles set name = coalesce($2, name), avatar_url = coalesce($3, avatar_url)
			where user_id = $1`, u.ID, req.Name, req.AvatarURL)
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.getMe(w, r)
}

const deleteConfirmation = "DELETE MY DATA"

// deleteMyData irreversibly deletes every memory, file, embedding, tag,
// conversation and share link. With delete_account the auth user is removed too.
func (s *Server) deleteMyData(w http.ResponseWriter, r *http.Request) {
	u := user(r)
	var req struct {
		Confirm       string `json:"confirm"`
		DeleteAccount bool   `json:"delete_account"`
	}
	if err := decode(r, &req); err != nil || req.Confirm != deleteConfirmation {
		writeError(w, http.StatusBadRequest, "confirmation_required", fmt.Sprintf("Type %q to confirm.", deleteConfirmation))
		return
	}
	// Survive client disconnects: once confirmed, deletion must finish.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Minute)
	defer cancel()

	paths, err := s.Store.ListAll(ctx, storage.UserPrefix(u.ID))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.Store.Delete(ctx, paths); err != nil {
		s.fail(w, r, err)
		return
	}
	err = s.DB.WithService(ctx, func(tx pgx.Tx) error {
		for _, q := range []string{
			`delete from processing_jobs where user_id = $1`,
			`delete from share_links where user_id = $1`,
			`delete from memory_chunks where user_id = $1`,
			`delete from memory_tags where user_id = $1`,
			`delete from memories where user_id = $1`,
			`delete from tags where user_id = $1`,
			`delete from messages where user_id = $1`,
			`delete from conversations where user_id = $1`,
			`delete from usage_counters where user_id = $1`,
		} {
			if _, err := tx.Exec(ctx, q, u.ID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// Files uploaded while we were deleting rows would be orphaned: sweep again.
	if late, err := s.Store.ListAll(ctx, storage.UserPrefix(u.ID)); err == nil && len(late) > 0 {
		_ = s.Store.Delete(ctx, late)
	}
	if req.DeleteAccount {
		if err := s.deleteAuthUser(ctx, u.ID); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	s.Log.Info("user data deleted", "files", len(paths), "account", req.DeleteAccount)
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "files_deleted": len(paths), "account_deleted": req.DeleteAccount})
}

func (s *Server) deleteAuthUser(ctx context.Context, userID string) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodDelete, s.Cfg.SupabaseURL+"/auth/v1/admin/users/"+userID, nil)
	req.Header.Set("Authorization", "Bearer "+s.Cfg.SupabaseServiceKey)
	req.Header.Set("apikey", s.Cfg.SupabaseServiceKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("delete auth user: status %d", resp.StatusCode)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Sharing a single memory
// ---------------------------------------------------------------------------

type share struct {
	ID        string     `json:"id"`
	URL       string     `json:"url,omitempty"`
	ExpiresAt time.Time  `json:"expires_at"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

func (s *Server) createShare(w http.ResponseWriter, r *http.Request) {
	u := user(r)
	var req struct {
		ExpiresInHours int `json:"expires_in_hours"`
	}
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if req.ExpiresInHours <= 0 {
		req.ExpiresInHours = 24
	}
	if req.ExpiresInHours > 24*30 {
		writeError(w, http.StatusBadRequest, "bad_request", "Share links can last at most 30 days.")
		return
	}
	m, err := s.loadMemory(r.Context(), u.ID, r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		s.fail(w, r, err)
		return
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	sh := share{ExpiresAt: time.Now().Add(time.Duration(req.ExpiresInHours) * time.Hour).UTC()}
	err = s.DB.WithUser(r.Context(), u.ID, func(tx pgx.Tx) error {
		return tx.QueryRow(r.Context(), `insert into share_links (user_id, memory_id, token_hash, expires_at)
			values ($1, $2, $3, $4) returning id, created_at`, u.ID, m.ID, sha256Hex(token), sh.ExpiresAt).Scan(&sh.ID, &sh.CreatedAt)
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	sh.URL = s.Cfg.PublicURL + "/shared/" + token
	writeJSON(w, http.StatusCreated, sh)
}

func (s *Server) listShares(w http.ResponseWriter, r *http.Request) {
	u := user(r)
	id := r.PathValue("id")
	if !uuidRe.MatchString(id) {
		writeError(w, http.StatusNotFound, "not_found", "Not found.")
		return
	}
	out := []share{}
	err := s.DB.WithUser(r.Context(), u.ID, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `select id, expires_at, revoked_at, created_at from share_links
			where memory_id = $1 and revoked_at is null and expires_at > now() order by created_at desc`, id)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var sh share
			if err := rows.Scan(&sh.ID, &sh.ExpiresAt, &sh.RevokedAt, &sh.CreatedAt); err != nil {
				return err
			}
			out = append(out, sh)
		}
		return rows.Err()
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"shares": out})
}

func (s *Server) revokeShare(w http.ResponseWriter, r *http.Request) {
	u := user(r)
	id := r.PathValue("id")
	if !uuidRe.MatchString(id) {
		writeError(w, http.StatusNotFound, "not_found", "Not found.")
		return
	}
	err := s.DB.WithUser(r.Context(), u.ID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(r.Context(), `update share_links set revoked_at = now() where id = $1 and revoked_at is null`, id)
		if err == nil && tag.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// getPublicShare returns one shared memory. Only that memory is exposed,
// and only while the link is unexpired and unrevoked.
func (s *Server) getPublicShare(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	if len(token) < 40 || len(token) > 60 {
		writeError(w, http.StatusNotFound, "not_found", "This link is invalid or has expired.")
		return
	}
	var userID, memoryID string
	var expires time.Time
	err := s.DB.Pool.QueryRow(r.Context(), `select user_id, memory_id, expires_at from share_links
		where token_hash = $1 and revoked_at is null and expires_at > now()`, sha256Hex(token)).Scan(&userID, &memoryID, &expires)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "This link is invalid or has expired.")
		return
	}
	m, err := memory.GetForUser(r.Context(), s.DB.Pool, userID, memoryID)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "This link is invalid or has expired.")
		return
	}
	s.sign(r.Context(), []*memory.Memory{m}, true)
	// Shared view: no internal metadata, hashes or ids.
	writeJSON(w, http.StatusOK, map[string]any{
		"memory": map[string]any{
			"type": m.Type, "title": m.Title, "summary": m.Summary, "content": m.Content,
			"tags": m.Tags, "source_url": m.SourceURL, "created_at": m.CreatedAt,
			"thumbnail_url": m.ThumbnailURL, "file_url": m.FileURL, "mime_type": m.MimeType,
		},
		"expires_at": expires,
	})
}
