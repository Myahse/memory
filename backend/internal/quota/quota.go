// Package quota enforces Free / Pro plan limits on the server.
package quota

import (
	"context"
	"errors"

	"github.com/myahse/memory/backend/internal/config"
	"github.com/myahse/memory/backend/internal/db"
)

var (
	ErrAIQueries  = errors.New("You've reached this month's AI question limit on the Free plan.")
	ErrProcessing = errors.New("You've reached this month's processing limit on the Free plan.")
	ErrStorage    = errors.New("Not enough storage left on your plan for this file.")
	ErrFileTooBig = errors.New("This file is larger than your plan allows.")
)

type Usage struct {
	Plan           string `json:"plan"`
	StorageUsed    int64  `json:"storage_used"`
	StorageLimit   int64  `json:"storage_limit"`
	AIQueries      int    `json:"ai_queries"`
	AIQueryLimit   int    `json:"ai_query_limit"` // 0 = unlimited
	Processed      int    `json:"processed_items"`
	ProcessedLimit int    `json:"processed_limit"` // 0 = unlimited
	MaxFileBytes   int64  `json:"max_file_bytes"`
	MemoryCount    int    `json:"memory_count"`
}

type Service struct {
	DB  *db.DB
	Cfg *config.Config
}

func (s *Service) plan(name string) config.Plan {
	if name == "pro" {
		return s.Cfg.Pro
	}
	return s.Cfg.Free
}

func (s *Service) Usage(ctx context.Context, userID string) (*Usage, error) {
	u := &Usage{}
	err := s.DB.Pool.QueryRow(ctx, `
		select coalesce((select plan::text from profiles where user_id = $1), 'free'),
		       coalesce((select sum(file_size) from memories where user_id = $1), 0)::bigint,
		       (select count(*) from memories where user_id = $1)::int,
		       coalesce((select ai_queries from usage_counters where user_id = $1
		                 and period = date_trunc('month', now() at time zone 'utc')::date), 0),
		       coalesce((select processed_items from usage_counters where user_id = $1
		                 and period = date_trunc('month', now() at time zone 'utc')::date), 0)`, userID).
		Scan(&u.Plan, &u.StorageUsed, &u.MemoryCount, &u.AIQueries, &u.Processed)
	if err != nil {
		return nil, err
	}
	p := s.plan(u.Plan)
	u.StorageLimit, u.AIQueryLimit, u.ProcessedLimit, u.MaxFileBytes = p.StorageBytes, max(p.AIQueries, 0), max(p.ProcessedItems, 0), p.MaxFileBytes
	return u, nil
}

// CheckUpload validates a new file of size bytes and the monthly processing limit.
func (s *Service) CheckUpload(ctx context.Context, userID string, size int64) error {
	u, err := s.Usage(ctx, userID)
	if err != nil {
		return err
	}
	if size > u.MaxFileBytes {
		return ErrFileTooBig
	}
	if u.StorageUsed+size > u.StorageLimit {
		return ErrStorage
	}
	if u.ProcessedLimit > 0 && u.Processed >= u.ProcessedLimit {
		return ErrProcessing
	}
	return nil
}

// UseAIQuery atomically consumes one AI query if the plan allows it.
func (s *Service) UseAIQuery(ctx context.Context, userID string) error {
	u, err := s.Usage(ctx, userID)
	if err != nil {
		return err
	}
	limit := u.AIQueryLimit
	if limit <= 0 {
		limit = 1 << 30
	}
	tag, err := s.DB.Pool.Exec(ctx, `
		insert into usage_counters (user_id, period, ai_queries)
		values ($1, date_trunc('month', now() at time zone 'utc')::date, 1)
		on conflict (user_id, period) do update set ai_queries = usage_counters.ai_queries + 1
		where usage_counters.ai_queries < $2`, userID, limit)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrAIQueries
	}
	return nil
}
