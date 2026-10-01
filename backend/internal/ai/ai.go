// Package ai hides every model provider behind small capability interfaces so
// providers can be swapped per capability (LLM, vision, speech, embeddings)
// through configuration only.
package ai

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
)

// Dimensions is the embedding size stored in Postgres (vector(1536)).
const Dimensions = 1536

type Attachment struct {
	MIME string
	Data []byte
}

type Message struct {
	Role string // "user" | "assistant"
	Text string
}

type CompletionRequest struct {
	System      string
	Messages    []Message
	Attachments []Attachment // attached to the final user message
	MaxTokens   int
	// Effort is a hint: "low" for extraction/classification, "medium" for answers.
	Effort string
}

// LLM generates text, optionally from images / PDFs.
type LLM interface {
	Name() string
	Complete(ctx context.Context, req CompletionRequest) (string, error)
	// Supports reports whether attachments of this MIME type are understood.
	Supports(mime string) bool
}

// Speech converts audio to text.
type Speech interface {
	Transcribe(ctx context.Context, audio []byte, mime string) (string, error)
}

// Embedder turns text into Dimensions-sized vectors.
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// Error carries the pipeline stage and whether a retry may help.
type Error struct {
	Stage     string
	Retryable bool
	Err       error
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %v", e.Stage, e.Err) }
func (e *Error) Unwrap() error { return e.Err }

// Wrap annotates err with a stage, classifying timeouts / network errors /
// rate limits / 5xx as retryable.
func Wrap(stage string, err error) error {
	if err == nil {
		return nil
	}
	var ae *Error
	if errors.As(err, &ae) {
		if ae.Stage == "" {
			ae.Stage = stage
		}
		return ae
	}
	return &Error{Stage: stage, Retryable: retryable(err), Err: err}
}

func retryable(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var ne net.Error
	if errors.As(err, &ne) {
		return true
	}
	var se *StatusError
	if errors.As(err, &se) {
		return se.Status == 408 || se.Status == 409 || se.Status == 429 || se.Status >= 500
	}
	return false
}

// StatusError is returned by HTTP-based providers.
type StatusError struct {
	Provider string
	Status   int
	Body     string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("%s: HTTP %d: %s", e.Provider, e.Status, truncate(e.Body, 300))
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func isImage(mime string) bool {
	switch mime {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
		return true
	}
	return false
}
