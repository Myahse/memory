package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/myahse/memory/backend/internal/ai"
	"github.com/myahse/memory/backend/internal/db"
	"github.com/myahse/memory/backend/internal/extract"
	"github.com/myahse/memory/backend/internal/memory"
	"github.com/myahse/memory/backend/internal/storage"
)

const (
	maxContentRunes = 200_000
	maxChunks       = 400
	chunkSize       = 1200
	chunkOverlap    = 200
)

// result is what extraction produced for one memory.
type result struct {
	Type      string
	Content   string
	Analysis  *ai.Analysis
	Thumbnail []byte
	Meta      map[string]any
	Captured  *time.Time
}

func (w *Worker) process(ctx context.Context, j *job) error {
	m, err := memory.GetForUser(ctx, w.DB.Pool, j.UserID, j.MemoryID)
	if db.IsNoRows(err) {
		return errMemoryGone
	}
	if err != nil {
		return err
	}

	var r *result
	if j.Type == "reembed" {
		r = &result{Type: m.Type, Content: m.Content, Meta: map[string]any{}, Analysis: &ai.Analysis{
			Title: m.Title, Summary: m.Summary, Category: m.Category, Tags: m.Tags,
		}}
	} else {
		r, err = w.extract(ctx, m)
		if err != nil {
			return err
		}
	}
	return w.index(ctx, j, m, r)
}

func (w *Worker) extract(ctx context.Context, m *memory.Memory) (*result, error) {
	now := w.Now()
	r := &result{Type: m.Type, Meta: map[string]any{}}
	switch m.Type {
	case "photo", "screenshot", "receipt":
		data, err := w.download(ctx, m)
		if err != nil {
			return nil, err
		}
		mime := sniffImage(data, m.MimeType)
		if thumb, err := extract.Thumbnail(data, 512); err == nil {
			r.Thumbnail = thumb
		} else {
			return nil, permanent("This image could not be read (%v). Supported formats: JPEG, PNG, WebP, GIF.", err)
		}
		a, err := w.AI.AnalyzeImage(ctx, data, mime, now)
		if err != nil {
			return nil, err
		}
		r.Analysis = a
		r.Content = joinNonEmpty("\n\n", a.Description, a.Text)
		if a.Kind == "photo" || a.Kind == "screenshot" || a.Kind == "receipt" {
			r.Type = a.Kind
		}
		if a.Receipt != nil {
			r.Meta["receipt"] = a.Receipt
			r.Type = "receipt"
		}

	case "pdf", "document":
		data, err := w.download(ctx, m)
		if err != nil {
			return nil, err
		}
		doc, err := extract.Text(data, m.MimeType)
		if errors.Is(err, extract.ErrUnsupported) {
			return nil, permanent("Unsupported document type (%s). Supported: PDF, DOCX, TXT, Markdown, CSV, HTML, RTF.", m.MimeType)
		}
		if err != nil {
			return nil, permanent("This document could not be read: %v", err)
		}
		text := doc.Text
		if m.MimeType == "application/pdf" {
			r.Meta["pages"] = doc.Pages
			if utf8.RuneCountInString(strings.TrimSpace(text)) < 50 {
				// Scanned PDF without a text layer: OCR it.
				ocr, err := w.AI.ReadDocument(ctx, data)
				if err != nil {
					return nil, err
				}
				text = ocr
				r.Meta["ocr"] = true
			}
		}
		if strings.TrimSpace(text) == "" {
			return nil, permanent("No text could be extracted from this document.")
		}
		r.Content = text
		a, err := w.AI.AnalyzeText(ctx, "document", text, now)
		if err != nil {
			return nil, err
		}
		if a.Title == "" {
			a.Title = doc.Title
		}
		if doc.Title != "" {
			r.Meta["document_title"] = doc.Title
		}
		if a.DocType != "" {
			r.Meta["document_type"] = a.DocType
		}
		if a.Author != "" {
			r.Meta["author"] = a.Author
		}
		r.Analysis = a

	case "voice":
		data, err := w.download(ctx, m)
		if err != nil {
			return nil, err
		}
		text, err := w.AI.Transcribe(ctx, data, m.MimeType)
		if err != nil {
			return nil, err
		}
		if text == "" {
			return nil, permanent("No speech was detected in this recording.")
		}
		r.Content = text
		a, err := w.AI.AnalyzeText(ctx, "voice note transcript", text, now)
		if err != nil {
			return nil, err
		}
		r.Analysis = a

	case "note":
		if strings.TrimSpace(m.Content) == "" {
			return nil, permanent("This note is empty.")
		}
		r.Content = m.Content
		a, err := w.AI.AnalyzeText(ctx, "note", m.Content, now)
		if err != nil {
			return nil, err
		}
		r.Analysis = a

	case "link":
		p, err := extract.FetchLink(ctx, m.SourceURL)
		if err != nil {
			// The link itself is still a useful memory: index the URL and any
			// user-provided text instead of failing.
			r.Meta["fetch_error"] = err.Error()
			p = &extract.LinkPreview{URL: m.SourceURL}
		}
		r.Meta["link"] = p
		r.Content = joinNonEmpty("\n\n", m.Content, p.Title, p.Description, p.Text)
		if r.Content == "" {
			r.Content = m.SourceURL
		}
		a, err := w.AI.AnalyzeText(ctx, "web page saved as a link ("+m.SourceURL+")", r.Content, now)
		if err != nil {
			return nil, err
		}
		if a.Title == "" {
			a.Title = p.Title
		}
		r.Analysis = a
		if p.ImageURL != "" {
			if img, err := extract.FetchImage(ctx, p.ImageURL, 5<<20); err == nil {
				if thumb, err := extract.Thumbnail(img, 512); err == nil {
					r.Thumbnail = thumb
				}
			}
		}
		// Keep the user's own note about the link plus the page text.

	default:
		return nil, permanent("Unknown memory type %q", m.Type)
	}

	if r.Analysis != nil {
		r.Meta["entities"] = r.Analysis.Entities
		if len(r.Analysis.Dates) > 0 {
			r.Meta["dates"] = r.Analysis.Dates
		}
		if r.Analysis.Language != "" {
			r.Meta["language"] = r.Analysis.Language
		}
		if r.Analysis.Description != "" {
			r.Meta["description"] = r.Analysis.Description
		}
	}
	return r, nil
}

func (w *Worker) download(ctx context.Context, m *memory.Memory) ([]byte, error) {
	if m.FilePath == "" {
		return nil, permanent("The file for this memory was never uploaded.")
	}
	data, ct, err := w.Store.Download(ctx, m.FilePath, w.Cfg.Pro.MaxFileBytes)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return nil, err
		}
		if strings.Contains(err.Error(), "exceeds") {
			return nil, permanent("This file is too large to process.")
		}
		return nil, fmt.Errorf("download original: %w", err)
	}
	if m.MimeType == "" {
		m.MimeType = ct
	}
	return data, nil
}

// index merges results (respecting user edits), chunks, embeds and stores.
func (w *Worker) index(ctx context.Context, j *job, m *memory.Memory, r *result) error {
	edited := func(f string) bool { return slices.Contains(m.UserEdited, f) }
	a := r.Analysis
	if a == nil {
		a = &ai.Analysis{}
	}

	title, summary, category, content, typ := m.Title, m.Summary, m.Category, m.Content, m.Type
	tags := m.Tags
	if !edited("title") && a.Title != "" {
		title = a.Title
	}
	if title == "" {
		title = defaultTitle(r.Type)
	}
	if !edited("summary") && a.Summary != "" {
		summary = a.Summary
	}
	if !edited("category") && a.Category != "" {
		category = a.Category
	}
	if !edited("tags") && len(a.Tags) > 0 {
		tags = a.Tags
	}
	if !edited("content") && r.Content != "" {
		content = r.Content
	}
	if !edited("type") && isImageType(m.Type) && isImageType(r.Type) {
		typ = r.Type
	}
	if utf8.RuneCountInString(content) > maxContentRunes {
		content = string([]rune(content)[:maxContentRunes])
		r.Meta["truncated"] = true
	}
	captured := m.CapturedAt
	if captured == nil && a.Receipt != nil && a.Receipt.Date != "" {
		if t, err := time.Parse("2006-01-02", a.Receipt.Date); err == nil {
			captured = &t
		}
	}

	// Chunk the full content; each chunk carries the title for context.
	chunks := extractChunks(content)
	if len(chunks) > maxChunks {
		chunks = chunks[:maxChunks]
		r.Meta["chunks_truncated"] = true
	}
	texts := make([]string, 0, len(chunks)+1)
	texts = append(texts, memoryEmbeddingText(typ, title, summary, category, tags, content))
	for _, c := range chunks {
		texts = append(texts, title+"\n"+c)
	}
	vecs, err := w.AI.Embed(ctx, texts)
	if err != nil {
		return err
	}

	// Merge new metadata into existing metadata (client-provided keys survive).
	meta := map[string]any{}
	_ = json.Unmarshal(m.Metadata, &meta)
	for k, v := range r.Meta {
		meta[k] = v
	}
	if dup := w.nearDuplicate(ctx, m, vecs[0]); dup != "" {
		meta["possible_duplicate_of"] = dup
	} else {
		delete(meta, "possible_duplicate_of")
	}
	metaJSON, _ := json.Marshal(meta)

	thumbPath := m.ThumbnailPath
	if r.Thumbnail != nil {
		thumbPath = storage.ThumbnailPath(m.UserID, m.ID)
		if err := w.Store.Upload(ctx, thumbPath, "image/jpeg", r.Thumbnail); err != nil {
			return fmt.Errorf("upload thumbnail: %w", err)
		}
	}

	return w.DB.WithService(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			update memories set title = $3, summary = nullif($4,''), category = nullif($5,''), content = nullif($6,''),
			  type = $7::memory_type, metadata = $8, thumbnail_path = nullif($9,''), embedding = $10::extensions.vector,
			  captured_at = $11, status = 'ready', processing_error = null
			where id = $1 and user_id = $2`,
			m.ID, m.UserID, title, summary, category, content, typ, string(metaJSON), thumbPath, vectorLiteral(vecs[0]), captured)
		if err != nil {
			return fmt.Errorf("update memory: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return errMemoryGone
		}
		if _, err := tx.Exec(ctx, `delete from memory_chunks where memory_id = $1 and user_id = $2`, m.ID, m.UserID); err != nil {
			return err
		}
		batch := &pgx.Batch{}
		for i, c := range chunks {
			batch.Queue(`insert into memory_chunks (memory_id, user_id, content, chunk_index, embedding)
				values ($1, $2, $3, $4, $5::extensions.vector)`, m.ID, m.UserID, c, i, vectorLiteral(vecs[i+1]))
		}
		if err := tx.SendBatch(ctx, batch).Close(); err != nil {
			return fmt.Errorf("insert chunks: %w", err)
		}
		if !edited("tags") {
			if err := memory.SetTags(ctx, tx, m.UserID, m.ID, tags, "ai"); err != nil {
				return err
			}
			if err := memory.DeleteOrphanTags(ctx, tx, m.UserID); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `update processing_jobs set status = 'succeeded', completed_at = now(),
			locked_until = null, error = null where id = $1`, j.ID); err != nil {
			return err
		}
		if j.Type == "process" {
			_, err = tx.Exec(ctx, `insert into usage_counters (user_id, period, processed_items)
				values ($1, date_trunc('month', now() at time zone 'utc')::date, 1)
				on conflict (user_id, period) do update set processed_items = usage_counters.processed_items + 1`, m.UserID)
		}
		return err
	})
}

// nearDuplicate finds an existing memory whose embedding is almost identical
// (e.g. the same screenshot saved twice). It never deletes anything.
func (w *Worker) nearDuplicate(ctx context.Context, m *memory.Memory, vec []float32) string {
	if m.Type == "note" {
		return ""
	}
	var id string
	err := w.DB.Pool.QueryRow(ctx, `
		select id from memories
		where user_id = $1 and id <> $2 and embedding is not null and type = $3::memory_type
		  and 1 - (embedding <=> $4::extensions.vector) >= 0.97
		order by embedding <=> $4::extensions.vector limit 1`,
		m.UserID, m.ID, m.Type, vectorLiteral(vec)).Scan(&id)
	if err != nil {
		return ""
	}
	return id
}

func extractChunks(content string) []string {
	return extract.Chunk(content, chunkSize, chunkOverlap)
}

func memoryEmbeddingText(typ, title, summary, category string, tags []string, content string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %s\n", typ, title)
	if summary != "" {
		b.WriteString(summary + "\n")
	}
	if category != "" {
		b.WriteString("Category: " + category + "\n")
	}
	if len(tags) > 0 {
		b.WriteString("Tags: " + strings.Join(tags, ", ") + "\n")
	}
	r := []rune(content)
	if len(r) > 2000 {
		r = r[:2000]
	}
	b.WriteString(string(r))
	return b.String()
}

// vectorLiteral formats a vector for pgvector's text input.
func vectorLiteral(v []float32) string {
	var b strings.Builder
	b.Grow(len(v) * 10)
	b.WriteByte('[')
	for i, f := range v {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(float64(f), 'g', 7, 32))
	}
	b.WriteByte(']')
	return b.String()
}

// VectorLiteral is exported for the search package.
func VectorLiteral(v []float32) string { return vectorLiteral(v) }

func isImageType(t string) bool { return t == "photo" || t == "screenshot" || t == "receipt" }

func defaultTitle(t string) string {
	switch t {
	case "voice":
		return "Voice note"
	case "screenshot":
		return "Screenshot"
	case "receipt":
		return "Receipt"
	case "photo":
		return "Photo"
	case "link":
		return "Saved link"
	case "note":
		return "Note"
	default:
		return "Document"
	}
}

func joinNonEmpty(sep string, parts ...string) string {
	var out []string
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, sep)
}

func sniffImage(data []byte, declared string) string {
	switch {
	case len(data) > 3 && data[0] == 0xFF && data[1] == 0xD8:
		return "image/jpeg"
	case len(data) > 8 && string(data[1:4]) == "PNG":
		return "image/png"
	case len(data) > 12 && string(data[0:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return "image/webp"
	case len(data) > 6 && string(data[0:3]) == "GIF":
		return "image/gif"
	}
	return declared
}
