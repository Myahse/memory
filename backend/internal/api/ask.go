package api

import (
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/myahse/memory/backend/internal/memory"
	"github.com/myahse/memory/backend/internal/rag"
	"github.com/myahse/memory/backend/internal/search"
)

type searchRequest struct {
	Query string `json:"query"`
	search.Filters
	Limit int `json:"limit"`
}

func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	u := user(r)
	var req searchRequest
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if utf8.RuneCountInString(req.Query) > 500 {
		writeError(w, http.StatusBadRequest, "bad_request", "Search query is too long.")
		return
	}
	for _, t := range req.Types {
		if !memory.ValidType(t) {
			writeError(w, http.StatusBadRequest, "bad_request", "Unknown memory type.")
			return
		}
	}
	hits, err := s.Search.Search(r.Context(), u.ID, req.Query, req.Filters, req.Limit)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	ms := make([]*memory.Memory, len(hits))
	for i, h := range hits {
		h.Memory.Content = preview(h.Memory.Content)
		ms[i] = h.Memory
	}
	s.sign(r.Context(), ms, false)
	if hits == nil {
		hits = []search.Hit{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"query": req.Query, "results": hits, "total": len(hits)})
}

func (s *Server) ask(w http.ResponseWriter, r *http.Request) {
	u := user(r)
	var req rag.Request
	if err := decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	req.Question = strings.TrimSpace(req.Question)
	if req.Question == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "Ask a question first.")
		return
	}
	if utf8.RuneCountInString(req.Question) > 1000 {
		writeError(w, http.StatusBadRequest, "bad_request", "Question is too long.")
		return
	}
	if req.ConversationID != "" && !uuidRe.MatchString(req.ConversationID) {
		writeError(w, http.StatusNotFound, "not_found", "Conversation not found.")
		return
	}
	for _, id := range req.MemoryIDs {
		if !uuidRe.MatchString(id) {
			writeError(w, http.StatusBadRequest, "bad_request", "Invalid memory id.")
			return
		}
	}
	if err := s.Quota.UseAIQuery(r.Context(), u.ID); err != nil {
		s.fail(w, r, err)
		return
	}
	resp, err := s.RAG.Ask(r.Context(), u.ID, req)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	for _, m := range resp.Sources {
		m.Content = preview(m.Content)
	}
	s.sign(r.Context(), resp.Sources, false)
	writeJSON(w, http.StatusOK, resp)
}

type conversation struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (s *Server) listConversations(w http.ResponseWriter, r *http.Request) {
	u := user(r)
	out := []conversation{}
	err := s.DB.WithUser(r.Context(), u.ID, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `select id, coalesce(title,''), created_at, updated_at from conversations
			order by updated_at desc limit 50`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var c conversation
			if err := rows.Scan(&c.ID, &c.Title, &c.CreatedAt, &c.UpdatedAt); err != nil {
				return err
			}
			out = append(out, c)
		}
		return rows.Err()
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"conversations": out})
}

func (s *Server) getConversation(w http.ResponseWriter, r *http.Request) {
	u := user(r)
	id := r.PathValue("id")
	if !uuidRe.MatchString(id) {
		writeError(w, http.StatusNotFound, "not_found", "Not found.")
		return
	}
	type message struct {
		ID        string           `json:"id"`
		Role      string           `json:"role"`
		Content   string           `json:"content"`
		Sources   []*memory.Memory `json:"sources"`
		CreatedAt time.Time        `json:"created_at"`
		sourceIDs []string
	}
	var c conversation
	msgs := []*message{}
	var all []*memory.Memory
	err := s.DB.WithUser(r.Context(), u.ID, func(tx pgx.Tx) error {
		if err := tx.QueryRow(r.Context(), `select id, coalesce(title,''), created_at, updated_at from conversations where id = $1`, id).
			Scan(&c.ID, &c.Title, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return err
		}
		rows, err := tx.Query(r.Context(), `select id, role::text, content, source_memory_ids::text[], created_at
			from messages where conversation_id = $1 order by created_at`, id)
		if err != nil {
			return err
		}
		var ids []string
		for rows.Next() {
			m := &message{Sources: []*memory.Memory{}}
			if err := rows.Scan(&m.ID, &m.Role, &m.Content, &m.sourceIDs, &m.CreatedAt); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, m.sourceIDs...)
			msgs = append(msgs, m)
		}
		rows.Close()
		// Deleted memories simply disappear from the sources list.
		all, err = memory.GetMany(r.Context(), tx, ids)
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	for _, m := range all {
		m.Content = preview(m.Content)
	}
	s.sign(r.Context(), all, false)
	byID := map[string]*memory.Memory{}
	for _, m := range all {
		byID[m.ID] = m
	}
	for _, m := range msgs {
		for _, sid := range m.sourceIDs {
			if mm := byID[sid]; mm != nil {
				m.Sources = append(m.Sources, mm)
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"conversation": c, "messages": msgs})
}

func (s *Server) deleteConversation(w http.ResponseWriter, r *http.Request) {
	u := user(r)
	id := r.PathValue("id")
	if !uuidRe.MatchString(id) {
		writeError(w, http.StatusNotFound, "not_found", "Not found.")
		return
	}
	err := s.DB.WithUser(r.Context(), u.ID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(r.Context(), `delete from conversations where id = $1`, id)
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
