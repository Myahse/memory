package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// Service implements Memory's AI tasks on top of the provider interfaces.
type Service struct {
	LLM      LLM
	Vision   LLM
	Speech   Speech
	Embedder Embedder
	Timeout  time.Duration
}

type Entities struct {
	People        []string `json:"people,omitempty"`
	Organizations []string `json:"organizations,omitempty"`
	Products      []string `json:"products,omitempty"`
	Places        []string `json:"places,omitempty"`
	Merchants     []string `json:"merchants,omitempty"`
}

type DateRef struct {
	Date  string `json:"date"`  // ISO-8601 (YYYY-MM-DD) when resolvable
	Label string `json:"label"` // what the date refers to
}

type Receipt struct {
	Merchant string  `json:"merchant,omitempty"`
	Total    float64 `json:"total,omitempty"`
	Currency string  `json:"currency,omitempty"`
	Date     string  `json:"date,omitempty"`
}

// Analysis is the structured understanding of one memory.
type Analysis struct {
	Kind        string    `json:"kind,omitempty"` // photo | screenshot | receipt | document
	Title       string    `json:"title"`
	Summary     string    `json:"summary"`
	Description string    `json:"description,omitempty"`
	Text        string    `json:"text,omitempty"` // OCR / visible text
	Category    string    `json:"category"`
	Tags        []string  `json:"tags"`
	Entities    Entities  `json:"entities"`
	Dates       []DateRef `json:"dates,omitempty"`
	Receipt     *Receipt  `json:"receipt,omitempty"`
	DocType     string    `json:"document_type,omitempty"`
	Author      string    `json:"author,omitempty"`
	Language    string    `json:"language,omitempty"`
}

// Categories keeps the automatic categorisation consistent.
var Categories = []string{
	"Technology", "Shopping", "Finance", "Education", "Work", "Projects", "Health",
	"Travel", "Food", "Home", "Personal", "Entertainment", "Documents", "Ideas", "Other",
}

const privacyRules = `Privacy rules:
- Never infer sensitive personal characteristics (ethnicity, religion, health conditions, sexual orientation, political views, age estimates) from appearance.
- Refer to people generically ("a person", "two people") unless a name is visibly written in the content.
- Do not guess information that is not present. Prefer leaving a field empty to inventing it.`

func (s *Service) ctx(ctx context.Context) (context.Context, context.CancelFunc) {
	if s.Timeout <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, s.Timeout)
}

const analysisSchema = `{
  "kind": "photo | screenshot | receipt | document (images only)",
  "title": "short specific title, max 8 words",
  "summary": "1-2 sentence factual summary",
  "description": "what the image shows (images only)",
  "text": "all legible text, verbatim, in reading order (images only)",
  "category": "one of: ` + "%s" + `",
  "tags": ["3-8 short lowercase-agnostic tags"],
  "entities": {"people": [], "organizations": [], "products": [], "places": [], "merchants": []},
  "dates": [{"date": "YYYY-MM-DD", "label": "what happens / happened then"}],
  "receipt": {"merchant": "", "total": 0, "currency": "", "date": "YYYY-MM-DD"} or null,
  "document_type": "invoice | letter | form | article | contract | notes | ... (documents only)",
  "author": "",
  "language": "ISO 639-1 code"
}`

func schema() string { return fmt.Sprintf(analysisSchema, strings.Join(Categories, ", ")) }

// AnalyzeImage performs vision + OCR + metadata extraction in a single call.
func (s *Service) AnalyzeImage(ctx context.Context, img []byte, mime string, now time.Time) (*Analysis, error) {
	if s.Vision == nil || !s.Vision.Supports(mime) {
		return nil, &Error{Stage: "vision", Err: fmt.Errorf("image type %s is not supported", mime)}
	}
	ctx, cancel := s.ctx(ctx)
	defer cancel()
	out, err := s.Vision.Complete(ctx, CompletionRequest{
		System: "You are the indexing engine of Memory, a private personal archive. You turn a saved image into structured, searchable metadata.\n" + privacyRules,
		Messages: []Message{{Role: "user", Text: fmt.Sprintf(
			"Today is %s. Analyse this saved image. Decide whether it is a camera photo, a screenshot, or a receipt/invoice. "+
				"Transcribe every legible piece of text into \"text\". Extract objects, products, merchants, places, dates and other entities useful for later search.\n"+
				"Respond with ONLY a JSON object of this shape:\n%s", now.Format("2006-01-02"), schema())}},
		Attachments: []Attachment{{MIME: mime, Data: img}},
		MaxTokens:   4096,
		Effort:      "low",
	})
	if err != nil {
		return nil, Wrap("vision", err)
	}
	var a Analysis
	if err := decodeJSON(out, &a); err != nil {
		return nil, &Error{Stage: "vision", Retryable: true, Err: err}
	}
	a.normalize()
	return &a, nil
}

// AnalyzeText extracts title/summary/tags/entities from text content.
// kind describes the source: "note", "voice note transcript", "document", "web page".
func (s *Service) AnalyzeText(ctx context.Context, kind, text string, now time.Time) (*Analysis, error) {
	ctx, cancel := s.ctx(ctx)
	defer cancel()
	out, err := s.LLM.Complete(ctx, CompletionRequest{
		System: "You are the indexing engine of Memory, a private personal archive. You turn saved content into structured, searchable metadata.\n" + privacyRules,
		Messages: []Message{{Role: "user", Text: fmt.Sprintf(
			"Today is %s. The user saved the following %s. Resolve relative dates (\"next Friday\") to ISO dates using today's date.\n"+
				"Respond with ONLY a JSON object of this shape (omit image-only fields):\n%s\n\n<content>\n%s\n</content>",
			now.Format("2006-01-02 (Monday)"), kind, schema(), clip(text, 24000))}},
		MaxTokens: 2048,
		Effort:    "low",
	})
	if err != nil {
		return nil, Wrap("analysis", err)
	}
	var a Analysis
	if err := decodeJSON(out, &a); err != nil {
		return nil, &Error{Stage: "analysis", Retryable: true, Err: err}
	}
	a.normalize()
	return &a, nil
}

// ReadDocument OCRs a scanned PDF (no text layer) when the provider supports PDFs.
func (s *Service) ReadDocument(ctx context.Context, pdf []byte) (string, error) {
	if s.Vision == nil || !s.Vision.Supports("application/pdf") {
		return "", &Error{Stage: "ocr", Err: errors.New("this PDF has no text layer and the configured provider cannot OCR PDFs")}
	}
	ctx, cancel := s.ctx(ctx)
	defer cancel()
	out, err := s.Vision.Complete(ctx, CompletionRequest{
		System:      "You transcribe documents faithfully.",
		Messages:    []Message{{Role: "user", Text: "Transcribe all text in this document verbatim, preserving headings and reading order. Output only the text."}},
		Attachments: []Attachment{{MIME: "application/pdf", Data: pdf}},
		MaxTokens:   16000,
		Effort:      "low",
	})
	if err != nil {
		return "", Wrap("ocr", err)
	}
	return strings.TrimSpace(out), nil
}

func (s *Service) Transcribe(ctx context.Context, audio []byte, mime string) (string, error) {
	if s.Speech == nil {
		return "", &Error{Stage: "transcription", Err: errors.New("no speech provider configured")}
	}
	ctx, cancel := s.ctx(ctx)
	defer cancel()
	text, err := s.Speech.Transcribe(ctx, audio, mime)
	if err != nil {
		return "", Wrap("transcription", err)
	}
	return strings.TrimSpace(text), nil
}

func (s *Service) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	ctx, cancel := s.ctx(ctx)
	defer cancel()
	var out [][]float32
	for start := 0; start < len(texts); start += 64 {
		end := min(start+64, len(texts))
		batch := make([]string, 0, end-start)
		for _, t := range texts[start:end] {
			batch = append(batch, clip(t, 8000))
		}
		vecs, err := s.Embedder.Embed(ctx, batch)
		if err != nil {
			return nil, Wrap("embedding", err)
		}
		if len(vecs) != len(batch) {
			return nil, &Error{Stage: "embedding", Retryable: true, Err: fmt.Errorf("expected %d vectors, got %d", len(batch), len(vecs))}
		}
		for _, v := range vecs {
			if len(v) != Dimensions {
				return nil, &Error{Stage: "embedding", Err: fmt.Errorf("embedding has %d dimensions, database expects %d", len(v), Dimensions)}
			}
		}
		out = append(out, vecs...)
	}
	return out, nil
}

// QueryPlan is the result of query analysis.
type QueryPlan struct {
	SearchQuery string   `json:"search_query"`
	Keywords    []string `json:"keywords"`
	Types       []string `json:"types"`
	DateFrom    string   `json:"date_from"`
	DateTo      string   `json:"date_to"`
	Category    string   `json:"category"`
}

var memoryTypes = []string{"photo", "screenshot", "pdf", "document", "receipt", "voice", "note", "link"}

// AnalyzeQuery turns a natural-language question into a search plan.
func (s *Service) AnalyzeQuery(ctx context.Context, question string, now time.Time) (*QueryPlan, error) {
	ctx, cancel := s.ctx(ctx)
	defer cancel()
	out, err := s.LLM.Complete(ctx, CompletionRequest{
		System: "You convert questions about a personal archive into search parameters.",
		Messages: []Message{{Role: "user", Text: fmt.Sprintf(
			`Today is %s. Question: %q

Return ONLY JSON:
{"search_query": "the question rewritten as a concise standalone search query",
 "keywords": ["distinctive words or names that should appear literally"],
 "types": [subset of %s, only if the user clearly restricts the kind of item, else []],
 "date_from": "YYYY-MM-DD or empty", "date_to": "YYYY-MM-DD (exclusive) or empty — only when the question mentions a time period",
 "category": "one of %s only if clearly implied, else empty"}`,
			now.Format("2006-01-02 (Monday)"), question, strings.Join(memoryTypes, ", "), strings.Join(Categories, ", "))}},
		MaxTokens: 512,
		Effort:    "low",
	})
	if err != nil {
		return nil, Wrap("query analysis", err)
	}
	var p QueryPlan
	if err := decodeJSON(out, &p); err != nil {
		return nil, &Error{Stage: "query analysis", Err: err}
	}
	valid := p.Types[:0]
	for _, t := range p.Types {
		for _, mt := range memoryTypes {
			if t == mt {
				valid = append(valid, t)
			}
		}
	}
	p.Types = valid
	if strings.TrimSpace(p.SearchQuery) == "" {
		p.SearchQuery = question
	}
	return &p, nil
}

// ContextDoc is one retrieved memory given to the LLM.
type ContextDoc struct {
	Ref     int
	Type    string
	Title   string
	Date    time.Time
	Summary string
	Tags    []string
	Excerpt string
}

type Answer struct {
	Found   bool   `json:"found"`
	Answer  string `json:"answer"`
	Sources []int  `json:"sources"`
}

// Answer generates a grounded answer. Sources are validated against docs:
// the model can only cite memories that were actually retrieved.
func (s *Service) Answer(ctx context.Context, question string, history []Message, docs []ContextDoc, now time.Time) (*Answer, error) {
	var b strings.Builder
	for _, d := range docs {
		fmt.Fprintf(&b, "<memory ref=\"%d\" type=\"%s\" saved=\"%s\">\nTitle: %s\n", d.Ref, d.Type, d.Date.Format("2006-01-02"), d.Title)
		if d.Summary != "" {
			fmt.Fprintf(&b, "Summary: %s\n", d.Summary)
		}
		if len(d.Tags) > 0 {
			fmt.Fprintf(&b, "Tags: %s\n", strings.Join(d.Tags, ", "))
		}
		if d.Excerpt != "" {
			fmt.Fprintf(&b, "Content: %s\n", d.Excerpt)
		}
		b.WriteString("</memory>\n")
	}

	msgs := append([]Message{}, history...)
	msgs = append(msgs, Message{Role: "user", Text: fmt.Sprintf(
		`Today is %s.

These are the ONLY memories retrieved from my archive for this question:
%s
Question: %s

Answer using only these memories. Rules:
- Be concise: 1-3 sentences, mention concrete details (names, models, amounts, dates).
- Do not mention memories that are not listed above, and never invent details.
- If none of the memories answers the question, set "found" to false.
- "sources" lists the ref numbers of the memories you actually used.
- Do not put ref numbers or brackets in the answer text.
Respond with ONLY JSON: {"found": true|false, "answer": "...", "sources": [ref, ...]}`,
		now.Format("2006-01-02"), b.String(), question)})

	ctx, cancel := s.ctx(ctx)
	defer cancel()
	out, err := s.LLM.Complete(ctx, CompletionRequest{
		System:    "You are Memory, a private assistant that answers questions strictly from the user's saved memories. You never claim a memory exists unless it was provided to you.",
		Messages:  msgs,
		MaxTokens: 2048,
		Effort:    "medium",
	})
	if err != nil {
		return nil, Wrap("answer", err)
	}
	var a Answer
	if err := decodeJSON(out, &a); err != nil {
		return nil, &Error{Stage: "answer", Retryable: true, Err: err}
	}
	allowed := map[int]bool{}
	for _, d := range docs {
		allowed[d.Ref] = true
	}
	seen := map[int]bool{}
	var srcs []int
	for _, r := range a.Sources {
		if allowed[r] && !seen[r] {
			seen[r] = true
			srcs = append(srcs, r)
		}
	}
	a.Sources = srcs
	a.Answer = strings.TrimSpace(a.Answer)
	if !a.Found || len(a.Sources) == 0 || a.Answer == "" {
		return &Answer{Found: false}, nil
	}
	return &a, nil
}

func (a *Analysis) normalize() {
	a.Title = strings.TrimSpace(a.Title)
	a.Summary = strings.TrimSpace(a.Summary)
	a.Text = strings.TrimSpace(a.Text)
	cat := ""
	for _, c := range Categories {
		if strings.EqualFold(c, strings.TrimSpace(a.Category)) {
			cat = c
		}
	}
	if cat == "" && a.Category != "" {
		cat = "Other"
	}
	a.Category = cat
	seen := map[string]bool{}
	var tags []string
	for _, t := range a.Tags {
		t = strings.TrimSpace(strings.TrimPrefix(t, "#"))
		k := strings.ToLower(t)
		if t == "" || seen[k] || utf8.RuneCountInString(t) > 40 {
			continue
		}
		seen[k] = true
		tags = append(tags, t)
		if len(tags) == 10 {
			break
		}
	}
	a.Tags = tags
	switch a.Kind {
	case "photo", "screenshot", "receipt", "document", "":
	default:
		a.Kind = ""
	}
	if a.Receipt != nil && a.Receipt.Merchant == "" && a.Receipt.Total == 0 {
		a.Receipt = nil
	}
}

// decodeJSON extracts the first JSON object from model output.
func decodeJSON(s string, v any) error {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start < 0 || end < start {
		return fmt.Errorf("model did not return JSON")
	}
	if err := json.Unmarshal([]byte(s[start:end+1]), v); err != nil {
		return fmt.Errorf("model returned invalid JSON: %w", err)
	}
	return nil
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	// avoid cutting a UTF-8 sequence
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
