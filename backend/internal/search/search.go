// Package search implements hybrid (keyword + vector + metadata) retrieval.
package search

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/myahse/memory/backend/internal/ai"
	"github.com/myahse/memory/backend/internal/db"
	"github.com/myahse/memory/backend/internal/memory"
	"github.com/myahse/memory/backend/internal/pipeline"
)

type Filters struct {
	Types    []string   `json:"types"`
	Category string     `json:"category"`
	Tags     []string   `json:"tags"`
	From     *time.Time `json:"from"`
	To       *time.Time `json:"to"`
}

type Hit struct {
	Memory     *memory.Memory `json:"memory"`
	Score      float64        `json:"score"`
	Similarity *float64       `json:"similarity,omitempty"`
	Snippet    string         `json:"snippet,omitempty"`
	MatchedBy  []string       `json:"matched_by"`
}

type Searcher struct {
	DB  *db.DB
	AI  *ai.Service
	Log *slog.Logger
}

// Search runs hybrid retrieval as the given user (RLS enforced).
func (s *Searcher) Search(ctx context.Context, userID, query string, f Filters, limit int) ([]Hit, error) {
	query = strings.TrimSpace(query)
	if limit <= 0 || limit > 50 {
		limit = 20
	}

	// Embed the query; if embedding fails we still return keyword results.
	var vec any
	if query != "" {
		if v, err := s.AI.Embed(ctx, []string{query}); err == nil && len(v) == 1 {
			vec = pipeline.VectorLiteral(v[0])
		} else if err != nil {
			s.Log.Warn("query embedding failed; falling back to keyword search", "err", err)
		}
	}

	var hits []Hit
	err := s.DB.WithUser(ctx, userID, func(tx pgx.Tx) error {
		if query == "" {
			// No query: filtered browse ordered by recency.
			ms, err := memory.List(ctx, tx, memory.ListParams{Types: f.Types, Category: f.Category, From: f.From, To: f.To, Limit: limit,
				Tag: firstOr(f.Tags)})
			for _, m := range ms {
				hits = append(hits, Hit{Memory: m, MatchedBy: []string{"filter"}})
			}
			return err
		}
		var types, tags []string
		if len(f.Types) > 0 {
			types = f.Types
		}
		if len(f.Tags) > 0 {
			tags = f.Tags
		}
		rows, err := tx.Query(ctx, `select id, rrf_score, keyword_rank, vector_rank, similarity, coalesce(snippet,'')
			from search_memories($1, $2::extensions.vector, $3, $4::memory_type[], nullif($5,''), $6::text[], $7, $8)`,
			query, vec, limit, types, f.Category, tags, f.From, f.To)
		if err != nil {
			return err
		}
		type row struct {
			id      string
			score   float64
			krank   *int32
			vrank   *int32
			sim     *float64
			snippet string
		}
		var rs []row
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.id, &r.score, &r.krank, &r.vrank, &r.sim, &r.snippet); err != nil {
				rows.Close()
				return err
			}
			rs = append(rs, r)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		ids := make([]string, len(rs))
		for i, r := range rs {
			ids[i] = r.id
		}
		ms, err := memory.GetMany(ctx, tx, ids)
		if err != nil {
			return err
		}
		byID := map[string]*memory.Memory{}
		for _, m := range ms {
			byID[m.ID] = m
		}
		for _, r := range rs {
			m := byID[r.id]
			if m == nil {
				continue
			}
			h := Hit{Memory: m, Score: r.score, Similarity: r.sim, Snippet: r.snippet}
			if r.krank != nil {
				h.MatchedBy = append(h.MatchedBy, "keyword")
			}
			if r.vrank != nil {
				h.MatchedBy = append(h.MatchedBy, "semantic")
			}
			hits = append(hits, h)
		}
		return nil
	})
	return hits, err
}

func firstOr(s []string) string {
	if len(s) > 0 {
		return s[0]
	}
	return ""
}
