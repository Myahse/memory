// Command memory runs the Memory API and/or the processing worker.
//
//	MODE=api     HTTP API only
//	MODE=worker  background processing only
//	MODE=all     both (default; fine for small deployments)
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/myahse/memory/backend/internal/ai"
	"github.com/myahse/memory/backend/internal/api"
	"github.com/myahse/memory/backend/internal/auth"
	"github.com/myahse/memory/backend/internal/config"
	"github.com/myahse/memory/backend/internal/db"
	"github.com/myahse/memory/backend/internal/pipeline"
	"github.com/myahse/memory/backend/internal/quota"
	"github.com/myahse/memory/backend/internal/rag"
	"github.com/myahse/memory/backend/internal/search"
	"github.com/myahse/memory/backend/internal/storage"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if err := run(log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	database, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer database.Close()

	aiSvc, err := ai.FromConfig(cfg)
	if err != nil {
		return err
	}
	store := storage.New(cfg.SupabaseURL, cfg.SupabaseServiceKey, cfg.StorageBucket)
	log.Info("starting", "mode", cfg.Mode, "llm", aiSvc.LLM.Name(), "vision", aiSvc.Vision.Name())

	var wg sync.WaitGroup
	if cfg.Mode == "worker" || cfg.Mode == "all" {
		w := &pipeline.Worker{DB: database, Store: store, AI: aiSvc, Cfg: cfg, Log: log.With("component", "worker")}
		wg.Add(1)
		go func() { defer wg.Done(); w.Run(ctx) }()
	}

	if cfg.Mode == "api" || cfg.Mode == "all" {
		searcher := &search.Searcher{DB: database, AI: aiSvc, Log: log}
		srv := &api.Server{
			Cfg: cfg, DB: database, Store: store, AI: aiSvc,
			Auth:   auth.NewVerifier(cfg.SupabaseURL, cfg.SupabaseJWTSecret),
			Search: searcher,
			RAG:    &rag.Engine{DB: database, AI: aiSvc, Searcher: searcher, Log: log},
			Quota:  &quota.Service{DB: database, Cfg: cfg},
			Log:    log.With("component", "api"),
		}
		httpSrv := &http.Server{
			Addr:              cfg.Addr,
			Handler:           srv.Handler(),
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       30 * time.Second,
			WriteTimeout:      3 * time.Minute, // /v1/ask may wait on the LLM
			IdleTimeout:       2 * time.Minute,
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-ctx.Done()
			sctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			_ = httpSrv.Shutdown(sctx)
		}()
		log.Info("api listening", "addr", cfg.Addr)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			stop()
			wg.Wait()
			return err
		}
	}
	wg.Wait()
	return nil
}
