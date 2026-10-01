// Package db wraps the PostgreSQL pool.
//
// Request handlers never query as a privileged role: WithUser opens a
// transaction, switches to the `authenticated` role and sets the Supabase JWT
// claims so that every statement is checked by Row Level Security exactly as
// if the client had queried Supabase directly. Only the background worker and
// account deletion use the privileged connection (WithService), and they
// always filter by user_id explicitly.
package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type DB struct {
	Pool *pgxpool.Pool
}

func Open(ctx context.Context, url string) (*DB, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	// Supabase's pooler (transaction mode) does not support prepared statements.
	cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeExec
	// pgvector and pg_trgm live in the `extensions` schema on Supabase.
	cfg.ConnConfig.RuntimeParams["search_path"] = "public, extensions"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return &DB{Pool: pool}, nil
}

func (d *DB) Close() { d.Pool.Close() }

// WithUser runs fn in a transaction scoped to userID under RLS.
func (d *DB) WithUser(ctx context.Context, userID string, fn func(pgx.Tx) error) error {
	if userID == "" {
		return errors.New("db: empty user id")
	}
	claims, _ := json.Marshal(map[string]string{"sub": userID, "role": "authenticated"})
	return pgx.BeginFunc(ctx, d.Pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`select set_config('request.jwt.claims', $1, true), set_config('role', 'authenticated', true)`,
			string(claims)); err != nil {
			return fmt.Errorf("set rls context: %w", err)
		}
		return fn(tx)
	})
}

// WithService runs fn in a privileged transaction. Callers MUST scope every
// statement by user_id.
func (d *DB) WithService(ctx context.Context, fn func(pgx.Tx) error) error {
	return pgx.BeginFunc(ctx, d.Pool, fn)
}

// IsNoRows reports whether err is pgx.ErrNoRows.
func IsNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }
