// Package rag answers questions from the user's own memories.
//
//	QUESTION → QUERY ANALYSIS → HYBRID SEARCH (keyword + vector + metadata)
//	→ RANK → CONTEXT → LLM → ANSWER + VERIFIED SOURCES
//
// The model only ever sees retrieved memories, and its citations are checked
// against that set: it cannot reference a memory that was not retrieved.
package rag

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/myahse/memory/backend/internal/ai"
	"github.com/myahse/memory/backend/internal/db"
	"github.com/myahse/memory/backend/internal/memory"
	"github.com/myahse/memory/backend/internal/search"
)

const NotFound = "I couldn't find anything relevant in your Memory archive."

const (
	maxContextDocs = 8
	excerptRunes   = 1500
)

type Request struct {
	Question       string   `json:"question"`
	ConversationID string   `json:"conversation_id"`
	MemoryIDs      []string `json:"memory_ids"` // "Ask Memory about these results"
}

type Response struct {
	ConversationID string           `json:"conversation_id"`
	MessageID      string           `json:"message_id"`
	Answer         string           `json:"answer"`
	Found          bool             `json:"found"`
	Sources        []*memory.Memory `json:"sources"`
	Considered     int              `json:"considered"`
}

type Engine struct {
	DB       *db.DB
	AI       *ai.Service
	Searcher *search.Searcher
	Log      *slog.Logger
	Now      func() time.Time
}

func (e *Engine) Ask(ctx context.Context, userID string, req Request) (*Response, error) {
	now := time.Now()
	if e.Now != nil {
		now = e.Now()
	}
	question := strings.TrimSpace(req.Question)

	history, convID, err := e.history(ctx, userID, req.ConversationID, question)
	if err != nil {
		return nil, err
	}

	candidates, err := e.retrieve(ctx, userID, question, req.MemoryIDs, history, now)
	if err != nil {
		return nil, err
	}

	resp := &Response{ConversationID: convID, Considered: len(candidates), Sources: []*memory.Memory{}}
	if len(candidates) == 0 {
		resp.Answer = NotFound
	} else {
		docs := make([]ai.ContextDoc, len(candidates))
		for i, m := range candidates {
			docs[i] = ai.ContextDoc{
				Ref: i + 1, Type: m.Type, Title: m.Title, Summary: m.Summary, Tags: m.Tags,
				Date: dateOf(m), Excerpt: excerpt(m.Content),
			}
		}
		ans, err := e.AI.Answer(ctx, question, history, docs, now)
		if err != nil {
			return nil, err
		}
		if ans.Found {
			resp.Found = true
			resp.Answer = ans.Answer
			for _, ref := range ans.Sources {
				resp.Sources = append(resp.Sources, candidates[ref-1])
			}
		} else {
			resp.Answer = NotFound
		}
	}

	// Persist both turns with the verified source ids.
	srcIDs := make([]string, len(resp.Sources))
	for i, s := range resp.Sources {
		srcIDs[i] = s.ID
	}
	err = e.DB.WithUser(ctx, userID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `insert into messages (conversation_id, user_id, role, content) values ($1, $2, 'user', $3)`,
			convID, userID, question); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `insert into messages (conversation_id, user_id, role, content, source_memory_ids)
			values ($1, $2, 'assistant', $3, $4::uuid[]) returning id`, convID, userID, resp.Answer, srcIDs).Scan(&resp.MessageID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `update conversations set updated_at = now() where id = $1`, convID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func (e *Engine) history(ctx context.Context, userID, convID, question string) ([]ai.Message, string, error) {
	var hist []ai.Message
	err := e.DB.WithUser(ctx, userID, func(tx pgx.Tx) error {
		if convID == "" {
			title := question
			if r := []rune(title); len(r) > 80 {
				title = string(r[:80]) + "…"
			}
			return tx.QueryRow(ctx, `insert into conversations (user_id, title) values ($1, $2) returning id`, userID, title).Scan(&convID)
		}
		var exists bool
		if err := tx.QueryRow(ctx, `select exists(select 1 from conversations where id = $1)`, convID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return pgx.ErrNoRows
		}
		rows, err := tx.Query(ctx, `select role::text, content from (
			select role, content, created_at from messages where conversation_id = $1 order by created_at desc limit 6
		) t order by created_at`, convID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var m ai.Message
			if err := rows.Scan(&m.Role, &m.Text); err != nil {
				return err
			}
			hist = append(hist, m)
		}
		return rows.Err()
	})
	return hist, convID, err
}

func (e *Engine) retrieve(ctx context.Context, userID, question string, ids []string, history []ai.Message, now time.Time) ([]*memory.Memory, error) {
	if len(ids) > 0 {
		if len(ids) > 20 {
			ids = ids[:20]
		}
		var out []*memory.Memory
		err := e.DB.WithUser(ctx, userID, func(tx pgx.Tx) error {
			var err error
			out, err = memory.GetMany(ctx, tx, ids) // RLS drops ids that aren't the user's
			return err
		})
		if len(out) > maxContextDocs {
			out = out[:maxContextDocs]
		}
		return out, err
	}

	// Follow-up questions ("and the price?") need the previous question.
	q := question
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == "user" {
			q = history[i].Text + "\n" + question
			break
		}
	}

	f := search.Filters{}
	query := q
	if plan, err := e.AI.AnalyzeQuery(ctx, q, now); err == nil {
		query = plan.SearchQuery
		if len(plan.Keywords) > 0 {
			query += " " + strings.Join(plan.Keywords, " ")
		}
		f.Types = plan.Types
		f.Category = plan.Category
		if t, err := time.Parse("2006-01-02", plan.DateFrom); err == nil {
			f.From = &t
		}
		if t, err := time.Parse("2006-01-02", plan.DateTo); err == nil {
			f.To = &t
		}
	} else {
		e.Log.Warn("query analysis failed; using raw question", "err", err)
	}

	hits, err := e.Searcher.Search(ctx, userID, query, f, maxContextDocs)
	if err != nil {
		return nil, err
	}
	// Metadata filters from query analysis can be too strict: relax them once.
	if len(hits) == 0 && (len(f.Types) > 0 || f.Category != "" || f.From != nil || f.To != nil) {
		hits, err = e.Searcher.Search(ctx, userID, query, search.Filters{}, maxContextDocs)
		if err != nil {
			return nil, err
		}
	}
	out := make([]*memory.Memory, 0, len(hits))
	for _, h := range hits {
		if h.Memory.Status != "ready" && h.Memory.Content == "" && h.Memory.Title == "" {
			continue
		}
		out = append(out, h.Memory)
	}
	return out, nil
}

func dateOf(m *memory.Memory) time.Time {
	if m.CapturedAt != nil {
		return *m.CapturedAt
	}
	return m.CreatedAt
}

func excerpt(s string) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) > excerptRunes {
		return string(r[:excerptRunes]) + "…"
	}
	return string(r)
}
