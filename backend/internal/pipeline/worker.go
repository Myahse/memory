// Package pipeline runs the asynchronous processing of memories:
//
//	UPLOAD → MEMORY RECORD → JOB → EXTRACT → OCR / TRANSCRIPTION / VISION →
//	SUMMARY + TAGS → CHUNK → EMBEDDINGS → STORE VECTORS → STATUS = READY
//
// Jobs live in the processing_jobs table and are claimed with
// FOR UPDATE SKIP LOCKED, so any number of worker replicas can run.
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/myahse/memory/backend/internal/ai"
	"github.com/myahse/memory/backend/internal/config"
	"github.com/myahse/memory/backend/internal/db"
	"github.com/myahse/memory/backend/internal/storage"
)

type Worker struct {
	DB    *db.DB
	Store *storage.Client
	AI    *ai.Service
	Cfg   *config.Config
	Log   *slog.Logger
	Now   func() time.Time
}

type job struct {
	ID       string
	MemoryID string
	UserID   string
	Type     string
	Attempts int
}

// Run blocks until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) {
	if w.Now == nil {
		w.Now = time.Now
	}
	n := max(w.Cfg.WorkerConcurrency, 1)
	w.Log.Info("worker started", "concurrency", n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				j, err := w.claim(ctx)
				if err != nil {
					if ctx.Err() == nil {
						w.Log.Error("claim job", "err", err)
					}
					sleep(ctx, 5*time.Second)
					continue
				}
				if j == nil {
					sleep(ctx, w.Cfg.WorkerPoll)
					continue
				}
				w.handle(ctx, j)
			}
		}()
	}
	wg.Wait()
	w.Log.Info("worker stopped")
}

// RunOnce claims and processes a single job. It reports whether a job ran.
func (w *Worker) RunOnce(ctx context.Context) (bool, error) {
	if w.Now == nil {
		w.Now = time.Now
	}
	j, err := w.claim(ctx)
	if err != nil || j == nil {
		return false, err
	}
	w.handle(ctx, j)
	return true, nil
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

func (w *Worker) claim(ctx context.Context) (*job, error) {
	var j job
	err := w.DB.Pool.QueryRow(ctx, `
		update processing_jobs set status = 'running', attempts = attempts + 1, started_at = now(),
		       locked_until = now() + interval '10 minutes', error = null
		where id = (
		  select id from processing_jobs
		  where (status = 'queued' and run_after <= now())
		     or (status = 'running' and locked_until < now())
		  order by run_after
		  for update skip locked
		  limit 1)
		returning id, memory_id, user_id, job_type, attempts`).Scan(&j.ID, &j.MemoryID, &j.UserID, &j.Type, &j.Attempts)
	if db.IsNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &j, nil
}

func (w *Worker) handle(ctx context.Context, j *job) {
	log := w.Log.With("job", j.ID, "memory", j.MemoryID, "type", j.Type, "attempt", j.Attempts)
	start := time.Now()
	// A job may never hold its lock longer than its lease.
	jctx, cancel := context.WithTimeout(ctx, 9*time.Minute)
	defer cancel()

	if _, err := w.DB.Pool.Exec(ctx, `update memories set status = 'processing', processing_error = null
		where id = $1 and user_id = $2 and status <> 'processing'`, j.MemoryID, j.UserID); err != nil {
		log.Error("mark processing", "err", err)
	}

	err := w.process(jctx, j)
	if err == nil {
		log.Info("memory ready", "took", time.Since(start).Round(time.Millisecond))
		return
	}
	if errors.Is(err, errMemoryGone) {
		_, _ = w.DB.Pool.Exec(ctx, `update processing_jobs set status = 'failed', error = 'memory deleted', completed_at = now() where id = $1`, j.ID)
		return
	}

	retry := isRetryable(err) && j.Attempts < w.Cfg.MaxJobAttempts
	log.Warn("processing failed", "err", err, "retry", retry)
	if retry {
		backoff := time.Duration(30*math.Pow(2, float64(j.Attempts-1))) * time.Second
		_, dbErr := w.DB.Pool.Exec(ctx, `update processing_jobs set status = 'queued', error = $2,
			run_after = now() + $3::interval, locked_until = null where id = $1`,
			j.ID, err.Error(), fmt.Sprintf("%d seconds", int(backoff.Seconds())))
		if dbErr != nil {
			log.Error("requeue job", "err", dbErr)
		}
		return
	}
	msg := FriendlyError(err)
	dbErr := w.DB.WithService(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `update processing_jobs set status = 'failed', error = $2, completed_at = now(),
			locked_until = null where id = $1`, j.ID, err.Error()); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `update memories set status = 'failed', processing_error = $3
			where id = $1 and user_id = $2`, j.MemoryID, j.UserID, msg)
		return err
	})
	if dbErr != nil {
		log.Error("mark failed", "err", dbErr)
	}
}

var errMemoryGone = errors.New("memory no longer exists")

// PermanentError marks failures a retry cannot fix (bad file, unsupported type…).
type PermanentError struct{ Msg string }

func (e *PermanentError) Error() string { return e.Msg }

func permanent(format string, args ...any) error {
	return &PermanentError{Msg: fmt.Sprintf(format, args...)}
}

func isRetryable(err error) bool {
	var pe *PermanentError
	if errors.As(err, &pe) {
		return false
	}
	var ae *ai.Error
	if errors.As(err, &ae) {
		return ae.Retryable
	}
	if errors.Is(err, storage.ErrNotFound) {
		return false
	}
	return true // database / network hiccups
}

// FriendlyError produces the message shown to the user next to "Retry".
func FriendlyError(err error) string {
	var pe *PermanentError
	if errors.As(err, &pe) {
		return pe.Msg
	}
	if errors.Is(err, storage.ErrNotFound) {
		return "The uploaded file could not be found. Please upload it again."
	}
	var ae *ai.Error
	if errors.As(err, &ae) {
		timeout := errors.Is(ae.Err, context.DeadlineExceeded)
		switch {
		case timeout:
			return "The AI service took too long to respond. Please retry."
		case ae.Stage == "ocr":
			return "Text recognition failed: " + short(ae.Err)
		case ae.Stage == "transcription":
			return "Transcription failed: " + short(ae.Err)
		case ae.Stage == "embedding":
			return "Search indexing failed. Please retry."
		case ae.Stage == "vision":
			return "Image analysis failed: " + short(ae.Err)
		default:
			return "AI processing failed: " + short(ae.Err)
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "Processing timed out. Please retry."
	}
	return "Processing failed. Please retry."
}

func short(err error) string {
	s := err.Error()
	if i := strings.Index(s, ": HTTP"); i > 0 {
		s = s[i+2:]
	}
	if len(s) > 160 {
		s = s[:160] + "…"
	}
	return s
}
