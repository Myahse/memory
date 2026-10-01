package ai

import (
	"context"
	"testing"
	"time"
)

// fixedLLM returns a canned response.
type fixedLLM struct{ out string }

func (f fixedLLM) Name() string                                                { return "fixed" }
func (f fixedLLM) Supports(string) bool                                        { return true }
func (f fixedLLM) Complete(context.Context, CompletionRequest) (string, error) { return f.out, nil }

func TestAnswerDropsFabricatedSources(t *testing.T) {
	s := &Service{LLM: fixedLLM{`Sure! {"found": true, "answer": "You saved an HP Omen.", "sources": [2, 9, 2]}`}}
	docs := []ContextDoc{{Ref: 1, Title: "a"}, {Ref: 2, Title: "b"}}
	a, err := s.Answer(context.Background(), "q", nil, docs, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !a.Found || len(a.Sources) != 1 || a.Sources[0] != 2 {
		t.Fatalf("got %+v", a)
	}
}

func TestAnswerWithOnlyFabricatedSourcesIsNotFound(t *testing.T) {
	s := &Service{LLM: fixedLLM{`{"found": true, "answer": "Invented.", "sources": [7]}`}}
	a, err := s.Answer(context.Background(), "q", nil, []ContextDoc{{Ref: 1}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if a.Found {
		t.Fatalf("answer citing no retrieved memory must be not-found: %+v", a)
	}
}

func TestAnalysisNormalize(t *testing.T) {
	a := Analysis{Category: "technology", Tags: []string{"#Windows", "windows", " BitLocker ", ""}, Kind: "selfie"}
	a.normalize()
	if a.Category != "Technology" || len(a.Tags) != 2 || a.Tags[0] != "Windows" || a.Kind != "" {
		t.Fatalf("got %+v", a)
	}
	b := Analysis{Category: "Weird"}
	b.normalize()
	if b.Category != "Other" {
		t.Fatalf("unknown category should map to Other, got %q", b.Category)
	}
}

func TestDecodeJSONFenced(t *testing.T) {
	var v struct{ A int }
	if err := decodeJSON("```json\n{\"A\": 3}\n```", &v); err != nil || v.A != 3 {
		t.Fatalf("v=%v err=%v", v, err)
	}
	if err := decodeJSON("no json here", &v); err == nil {
		t.Fatal("expected error")
	}
}

func TestMockEmbeddingSimilarity(t *testing.T) {
	v, _ := Mock{}.Embed(context.Background(), []string{"laptop research", "laptops researched", "banana bread"})
	dot := func(a, b []float32) (s float32) {
		for i := range a {
			s += a[i] * b[i]
		}
		return
	}
	if dot(v[0], v[1]) <= dot(v[0], v[2]) {
		t.Fatal("related texts should be closer than unrelated ones")
	}
}
