package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// Mock is a deterministic, offline provider for local development and tests.
// Embeddings use feature hashing, so keyword-overlapping texts are similar.
type Mock struct{}

func (Mock) Name() string              { return "mock" }
func (Mock) Supports(mime string) bool { return isImage(mime) || mime == "application/pdf" }

func (Mock) Transcribe(_ context.Context, audio []byte, _ string) (string, error) {
	return fmt.Sprintf("Voice note (%d bytes). Configure a speech provider for real transcription.", len(audio)), nil
}

func (Mock) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		v := make([]float32, Dimensions)
		for _, w := range Tokens(t) {
			h := fnv.New32a()
			h.Write([]byte(w))
			x := h.Sum32()
			sign := float32(1)
			if x&1 == 1 {
				sign = -1
			}
			v[int(x>>1)%Dimensions] += sign
		}
		var norm float64
		for _, f := range v {
			norm += float64(f * f)
		}
		if norm == 0 {
			v[0] = 1
			norm = 1
		}
		n := float32(math.Sqrt(norm))
		for j := range v {
			v[j] /= n
		}
		out[i] = v
	}
	return out, nil
}

var refRe = regexp.MustCompile(`<memory ref="(\d+)"[^>]*>\nTitle: ([^\n]*)`)
var contentRe = regexp.MustCompile(`(?s)<content>\n(.*)\n</content>`)
var questionRe = regexp.MustCompile(`Question: "?([^"\n]*)"?`)

func (Mock) Complete(_ context.Context, req CompletionRequest) (string, error) {
	prompt := ""
	if n := len(req.Messages); n > 0 {
		prompt = req.Messages[n-1].Text
	}
	var out any
	switch {
	case strings.Contains(prompt, "<memory ref="):
		refs := refRe.FindAllStringSubmatch(prompt, -1)
		if len(refs) == 0 {
			out = map[string]any{"found": false, "answer": "", "sources": []int{}}
			break
		}
		var ids []int
		var titles []string
		for _, r := range refs {
			var id int
			fmt.Sscan(r[1], &id)
			ids = append(ids, id)
			titles = append(titles, r[2])
		}
		out = map[string]any{"found": true, "answer": "I found these related memories: " + strings.Join(titles, "; ") + ".", "sources": ids}
	case strings.Contains(prompt, `"search_query"`):
		q := ""
		if m := questionRe.FindStringSubmatch(prompt); m != nil {
			q = m[1]
		}
		out = map[string]any{"search_query": q, "keywords": Tokens(q), "types": []string{}}
	case strings.HasPrefix(prompt, "Transcribe all text"):
		return "Scanned document (mock OCR).", nil
	case len(req.Attachments) > 0:
		out = Analysis{Kind: "photo", Title: "Saved image", Summary: "An image saved to Memory.", Description: "Image (mock vision provider).", Category: "Personal", Tags: []string{"image"}}
	default:
		text := ""
		if m := contentRe.FindStringSubmatch(prompt); m != nil {
			text = m[1]
		}
		out = mockAnalysis(text)
	}
	b, _ := json.Marshal(out)
	return string(b), nil
}

func mockAnalysis(text string) Analysis {
	text = strings.TrimSpace(text)
	first := text
	if i := strings.IndexAny(first, ".\n!?"); i > 0 {
		first = first[:i]
	}
	words := strings.Fields(first)
	if len(words) > 8 {
		words = words[:8]
	}
	title := strings.Join(words, " ")
	if title == "" {
		title = "Untitled"
	}
	summary := text
	if len(summary) > 200 {
		summary = summary[:200] + "…"
	}
	freq := map[string]int{}
	for _, t := range Tokens(text) {
		if len(t) > 3 {
			freq[t]++
		}
	}
	type kv struct {
		k string
		v int
	}
	var kvs []kv
	for k, v := range freq {
		kvs = append(kvs, kv{k, v})
	}
	sort.Slice(kvs, func(i, j int) bool {
		if kvs[i].v != kvs[j].v {
			return kvs[i].v > kvs[j].v
		}
		return kvs[i].k < kvs[j].k
	})
	var tags []string
	for i := 0; i < len(kvs) && i < 5; i++ {
		tags = append(tags, kvs[i].k)
	}
	return Analysis{Title: title, Summary: summary, Category: "Personal", Tags: tags}
}

var stop = map[string]bool{"the": true, "and": true, "for": true, "that": true, "this": true, "with": true, "was": true, "what": true, "are": true, "you": true, "from": true, "have": true, "about": true, "did": true, "my": true, "i": true, "a": true, "an": true, "of": true, "to": true, "in": true, "on": true, "is": true, "it": true, "at": true}

// Tokens lowercases, splits on non-letters/digits, drops stop words and
// strips a plural "s".
func Tokens(s string) []string {
	var out []string
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if stop[w] {
			continue
		}
		if len(w) > 3 && strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss") {
			w = w[:len(w)-1]
		}
		out = append(out, w)
	}
	return out
}
