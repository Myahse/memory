// Package e2e exercises the whole backend (API, RLS, worker, hybrid search,
// RAG, sharing and deletion) against a real PostgreSQL + pgvector database
// with the mock AI provider and an in-memory fake of Supabase Storage.
//
// Run with:  TEST_DATABASE_URL=postgres://... go test ./internal/e2e/
// The database must have testdata/supabase_stub.sql and the migrations applied.
package e2e

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

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

const jwtSecret = "test-secret-test-secret-test-secret"

// fakeStorage implements the subset of the Supabase Storage API we use.
type fakeStorage struct {
	mu      sync.Mutex
	objects map[string][]byte
	types   map[string]string
}

func (f *fakeStorage) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := strings.TrimPrefix(r.URL.Path, "/storage/v1")
	const bucket = "memories/"
	switch {
	case r.Method == http.MethodPost && strings.HasPrefix(p, "/object/upload/sign/"+bucket):
		path := strings.TrimPrefix(p, "/object/upload/sign/"+bucket)
		json.NewEncoder(w).Encode(map[string]string{"url": "/object/upload/sign/" + bucket + path + "?token=t"})
	case r.Method == http.MethodPut && strings.HasPrefix(p, "/object/upload/sign/"+bucket):
		path := strings.TrimPrefix(p, "/object/upload/sign/"+bucket)
		b, _ := io.ReadAll(r.Body)
		f.objects[path] = b
		f.types[path] = r.Header.Get("Content-Type")
		w.Write([]byte(`{}`))
	case r.Method == http.MethodPost && strings.HasPrefix(p, "/object/sign/"+bucket):
		path := strings.TrimPrefix(p, "/object/sign/"+bucket)
		json.NewEncoder(w).Encode(map[string]string{"signedURL": "/object/sign/" + bucket + path + "?token=s"})
	case r.Method == http.MethodPost && p == "/object/sign/memories":
		var req struct{ Paths []string }
		json.NewDecoder(r.Body).Decode(&req)
		var out []map[string]any
		for _, path := range req.Paths {
			u := "/object/sign/" + bucket + path + "?token=s"
			out = append(out, map[string]any{"path": path, "signedURL": u})
		}
		json.NewEncoder(w).Encode(out)
	case (r.Method == http.MethodGet || r.Method == http.MethodHead) && strings.HasPrefix(p, "/object/authenticated/"+bucket):
		path := strings.TrimPrefix(p, "/object/authenticated/"+bucket)
		b, ok := f.objects[path]
		if !ok {
			http.Error(w, `{"error":"not_found"}`, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", f.types[path])
		w.Header().Set("Content-Length", fmt.Sprint(len(b)))
		if r.Method == http.MethodGet {
			w.Write(b)
		}
	case r.Method == http.MethodPost && strings.HasPrefix(p, "/object/"+bucket):
		path := strings.TrimPrefix(p, "/object/"+bucket)
		b, _ := io.ReadAll(r.Body)
		f.objects[path] = b
		f.types[path] = r.Header.Get("Content-Type")
		w.Write([]byte(`{}`))
	case r.Method == http.MethodDelete && p == "/object/memories":
		var req struct{ Prefixes []string }
		json.NewDecoder(r.Body).Decode(&req)
		for _, path := range req.Prefixes {
			delete(f.objects, path)
		}
		w.Write([]byte(`[]`))
	case r.Method == http.MethodPost && p == "/object/list/memories":
		var req struct {
			Prefix string
			Offset int
		}
		json.NewDecoder(r.Body).Decode(&req)
		// Flat listing is enough: report every object under the prefix as a file.
		var out []map[string]any
		if req.Offset == 0 {
			for path := range f.objects {
				if strings.HasPrefix(path, req.Prefix+"/") {
					id := "x"
					out = append(out, map[string]any{"name": strings.TrimPrefix(path, req.Prefix+"/"), "id": id})
				}
			}
		}
		if out == nil {
			out = []map[string]any{}
		}
		json.NewEncoder(w).Encode(out)
	default:
		http.Error(w, "unexpected "+r.Method+" "+p, http.StatusTeapot)
	}
}

func (f *fakeStorage) count(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for p := range f.objects {
		if strings.HasPrefix(p, prefix) {
			n++
		}
	}
	return n
}

type env struct {
	t      *testing.T
	api    *httptest.Server
	store  *fakeStorage
	worker *pipeline.Worker
	db     *db.DB
}

func setup(t *testing.T) *env {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	database, err := db.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(database.Close)
	if _, err := database.Pool.Exec(ctx, `truncate auth.users cascade; truncate processing_jobs`); err != nil {
		t.Fatal(err)
	}

	fs := &fakeStorage{objects: map[string][]byte{}, types: map[string]string{}}
	storageSrv := httptest.NewServer(http.StripPrefix("", fs))
	t.Cleanup(storageSrv.Close)

	cfg := &config.Config{
		SupabaseURL: storageSrv.URL, SupabaseServiceKey: "service", SupabaseJWTSecret: jwtSecret,
		PublicURL: "https://memory.test", CORSOrigins: []string{"*"}, MaxJobAttempts: 1,
		Free: config.Plan{StorageBytes: 1 << 30, AIQueries: 50, ProcessedItems: 200, MaxFileBytes: 25 << 20},
		Pro:  config.Plan{StorageBytes: 1 << 34, MaxFileBytes: 100 << 20},
	}
	var logOut io.Writer = io.Discard
	if os.Getenv("TEST_VERBOSE") != "" {
		logOut = os.Stderr
	}
	log := slog.New(slog.NewTextHandler(logOut, nil))
	aiSvc := &ai.Service{LLM: ai.Mock{}, Vision: ai.Mock{}, Speech: ai.Mock{}, Embedder: ai.Mock{}, Timeout: 10 * time.Second}
	store := storage.New(storageSrv.URL, "service", "memories")
	searcher := &search.Searcher{DB: database, AI: aiSvc, Log: log}
	srv := &api.Server{
		Cfg: cfg, DB: database, Store: store, AI: aiSvc,
		Auth: auth.NewVerifier(storageSrv.URL, jwtSecret), Search: searcher,
		RAG:   &rag.Engine{DB: database, AI: aiSvc, Searcher: searcher, Log: log},
		Quota: &quota.Service{DB: database, Cfg: cfg}, Log: log,
	}
	apiSrv := httptest.NewServer(srv.Handler())
	t.Cleanup(apiSrv.Close)
	return &env{t: t, api: apiSrv, store: fs, db: database,
		worker: &pipeline.Worker{DB: database, Store: store, AI: aiSvc, Cfg: cfg, Log: log}}
}

func (e *env) newUser(email string) string {
	id := uuid.NewString()
	if _, err := e.db.Pool.Exec(context.Background(), `insert into auth.users (id, email, raw_user_meta_data) values ($1, $2, '{"name":"Test"}')`, id, email); err != nil {
		e.t.Fatal(err)
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": id, "email": email, "role": "authenticated", "aud": "authenticated",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	s, err := tok.SignedString([]byte(jwtSecret))
	if err != nil {
		e.t.Fatal(err)
	}
	return s
}

func (e *env) call(token, method, path string, body any, wantStatus int) map[string]any {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.api.URL+path, rd)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != wantStatus {
		e.t.Fatalf("%s %s: status %d, want %d: %s", method, path, resp.StatusCode, wantStatus, raw)
	}
	out := map[string]any{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	return out
}

func (e *env) drain() {
	e.t.Helper()
	for i := 0; i < 50; i++ {
		ran, err := e.worker.RunOnce(context.Background())
		if err != nil {
			e.t.Fatal(err)
		}
		if !ran {
			return
		}
	}
}

func (e *env) upload(token, typ, mime string, data []byte) string {
	e.t.Helper()
	h := sha256.Sum256(data)
	res := e.call(token, "POST", "/v1/memories", map[string]any{
		"type": typ, "mime_type": mime, "file_size": len(data), "content_hash": hex.EncodeToString(h[:]),
	}, http.StatusCreated)
	id := res["memory"].(map[string]any)["id"].(string)
	up := res["upload"].(map[string]any)
	req, _ := http.NewRequest(http.MethodPut, up["url"].(string), bytes.NewReader(data))
	req.Header.Set("Content-Type", mime)
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		e.t.Fatalf("upload failed: %v %v", err, resp)
	}
	resp.Body.Close()
	e.call(token, "POST", "/v1/memories/"+id+"/upload-complete", nil, http.StatusOK)
	return id
}

func pngBytes() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 800, 600))
	for x := 0; x < 800; x++ {
		img.Set(x, x%600, color.RGBA{200, 30, 30, 255})
	}
	var b bytes.Buffer
	png.Encode(&b, img)
	return b.Bytes()
}

func memoryOf(res map[string]any) map[string]any { return res["memory"].(map[string]any) }

func titles(results []any) []string {
	var out []string
	for _, r := range results {
		out = append(out, r.(map[string]any)["memory"].(map[string]any)["title"].(string))
	}
	return out
}

func TestEndToEnd(t *testing.T) {
	e := setup(t)
	alice := e.newUser("alice@example.com")
	bob := e.newUser("bob@example.com")

	// Unauthenticated access is rejected.
	e.call("", "GET", "/v1/memories", nil, http.StatusUnauthorized)

	// Profile was created by the auth trigger.
	me := e.call(alice, "GET", "/v1/me", nil, http.StatusOK)
	if me["profile"].(map[string]any)["name"] != "Test" {
		t.Fatalf("profile: %v", me)
	}

	// 1. Text note → processed asynchronously.
	note := memoryOf(e.call(alice, "POST", "/v1/memories", map[string]any{
		"type": "note", "content": "My laptop model is HP Omen 16 Max with an RTX 5080 graphics card.", "client_id": "offline-1",
	}, http.StatusCreated))
	if note["status"] != "pending" {
		t.Fatalf("note status = %v", note["status"])
	}
	// Offline retry with the same client_id is idempotent.
	again := memoryOf(e.call(alice, "POST", "/v1/memories", map[string]any{
		"type": "note", "content": "My laptop model is HP Omen 16 Max with an RTX 5080 graphics card.", "client_id": "offline-1",
	}, http.StatusOK))
	if again["id"] != note["id"] {
		t.Fatal("client_id retry created a second memory")
	}

	// 2. Document upload (plain text receipt).
	docID := e.upload(alice, "document", "text/plain", []byte("Carrefour supermarket receipt. Bananas, milk, bread. Total 45.20 EUR."))
	// 3. Photo upload.
	photoID := e.upload(alice, "photo", "image/png", pngBytes())
	// 4. Voice note.
	voiceID := e.upload(alice, "voice", "audio/mp4", []byte("fake-audio-bytes"))

	e.drain()

	for _, id := range []string{note["id"].(string), docID, photoID, voiceID} {
		m := memoryOf(e.call(alice, "GET", "/v1/memories/"+id, nil, http.StatusOK))
		if m["status"] != "ready" {
			t.Fatalf("memory %s status %v: %v", id, m["status"], m["processing_error"])
		}
	}
	photo := memoryOf(e.call(alice, "GET", "/v1/memories/"+photoID, nil, http.StatusOK))
	if photo["thumbnail_url"] == "" || photo["file_url"] == "" || e.store.count("users/") != 4 {
		t.Fatalf("photo files/thumbnail missing: %v", photo)
	}

	// Keyword search.
	res := e.call(alice, "POST", "/v1/search", map[string]any{"query": "Carrefour"}, http.StatusOK)
	got := titles(res["results"].([]any))
	if len(got) == 0 || !strings.Contains(strings.ToLower(got[0]), "carrefour") {
		t.Fatalf("keyword search: %v", got)
	}
	// Semantic search (feature-hashed mock embeddings) with a type filter.
	res = e.call(alice, "POST", "/v1/search", map[string]any{"query": "laptop graphics", "types": []string{"note"}}, http.StatusOK)
	if got := titles(res["results"].([]any)); len(got) != 1 || !strings.Contains(got[0], "laptop") {
		t.Fatalf("filtered search: %v", got)
	}

	// Ask: grounded answer with verified sources.
	ans := e.call(alice, "POST", "/v1/ask", map[string]any{"question": "What laptop model do I have?"}, http.StatusOK)
	if ans["found"] != true || len(ans["sources"].([]any)) == 0 {
		t.Fatalf("ask: %v", ans)
	}
	convID := ans["conversation_id"].(string)
	// Nothing relevant → explicit not-found, no sources.
	ans = e.call(alice, "POST", "/v1/ask", map[string]any{"question": "passport renewal appointment"}, http.StatusOK)
	if ans["found"] != false || ans["answer"] != rag.NotFound || len(ans["sources"].([]any)) != 0 {
		t.Fatalf("ask not found: %v", ans)
	}
	conv := e.call(alice, "GET", "/v1/conversations/"+convID, nil, http.StatusOK)
	if len(conv["messages"].([]any)) != 2 {
		t.Fatalf("conversation messages: %v", conv)
	}

	// Duplicate detection: same note again without a client id.
	dup := e.call(alice, "POST", "/v1/memories", map[string]any{
		"type": "note", "content": "My laptop model is HP Omen 16 Max with an RTX 5080 graphics card.",
	}, http.StatusConflict)
	if len(dup["duplicates"].([]any)) != 1 {
		t.Fatalf("duplicates: %v", dup)
	}

	// Edit: user edits survive reprocessing.
	e.call(alice, "PATCH", "/v1/memories/"+docID, map[string]any{"title": "Carrefour groceries", "tags": []string{"groceries", "food"}}, http.StatusOK)
	e.drain()
	doc := memoryOf(e.call(alice, "GET", "/v1/memories/"+docID, nil, http.StatusOK))
	if doc["title"] != "Carrefour groceries" || len(doc["tags"].([]any)) != 2 {
		t.Fatalf("edit: %v", doc)
	}

	// Privacy: Bob sees nothing of Alice's.
	e.call(bob, "GET", "/v1/memories/"+docID, nil, http.StatusNotFound)
	e.call(bob, "PATCH", "/v1/memories/"+docID, map[string]any{"title": "hacked"}, http.StatusNotFound)
	e.call(bob, "DELETE", "/v1/memories/"+docID, nil, http.StatusNotFound)
	e.call(bob, "GET", "/v1/conversations/"+convID, nil, http.StatusNotFound)
	if r := e.call(bob, "POST", "/v1/search", map[string]any{"query": "Carrefour"}, http.StatusOK); len(r["results"].([]any)) != 0 {
		t.Fatal("bob can search alice's memories")
	}
	if r := e.call(bob, "GET", "/v1/memories", nil, http.StatusOK); len(r["memories"].([]any)) != 0 {
		t.Fatal("bob can list alice's memories")
	}
	if r := e.call(bob, "POST", "/v1/ask", map[string]any{"question": "Carrefour receipt", "memory_ids": []string{docID}}, http.StatusOK); r["found"] != false {
		t.Fatal("bob got an answer from alice's memory")
	}
	if r := e.call(bob, "GET", "/v1/memories/"+docID+"/related", nil, http.StatusOK); len(r["related"].([]any)) != 0 {
		t.Fatal("bob sees related memories of alice")
	}

	// Sharing a single memory, then revoking it.
	sh := e.call(alice, "POST", "/v1/memories/"+docID+"/shares", map[string]any{"expires_in_hours": 1}, http.StatusCreated)
	token := strings.TrimPrefix(sh["url"].(string), "https://memory.test/shared/")
	pub := e.call("", "GET", "/public/shares/"+token, nil, http.StatusOK)
	if memoryOf(pub)["title"] != "Carrefour groceries" {
		t.Fatalf("public share: %v", pub)
	}
	e.call(bob, "DELETE", "/v1/shares/"+sh["id"].(string), nil, http.StatusNotFound)
	e.call(alice, "DELETE", "/v1/shares/"+sh["id"].(string), nil, http.StatusNoContent)
	e.call("", "GET", "/public/shares/"+token, nil, http.StatusNotFound)

	// Timeline + facets.
	tl := e.call(alice, "GET", "/v1/timeline", nil, http.StatusOK)
	if len(tl["groups"].([]any)) != 1 {
		t.Fatalf("timeline: %v", tl)
	}
	e.call(alice, "GET", "/v1/facets", nil, http.StatusOK)

	// Delete one memory → row and files gone.
	e.call(alice, "DELETE", "/v1/memories/"+photoID, nil, http.StatusNoContent)
	e.call(alice, "GET", "/v1/memories/"+photoID, nil, http.StatusNotFound)
	if n := e.store.count(storage.OriginalPath("", "")[:6]); n == 0 {
		t.Fatal("expected remaining objects for other memories")
	}

	// Delete everything requires the exact confirmation phrase.
	e.call(alice, "DELETE", "/v1/me/data", map[string]any{"confirm": "yes"}, http.StatusBadRequest)
	e.call(alice, "DELETE", "/v1/me/data", map[string]any{"confirm": "DELETE MY DATA"}, http.StatusOK)
	var remaining int
	aliceID := me["id"].(string)
	e.db.Pool.QueryRow(context.Background(), `select (select count(*) from memories where user_id = $1)
		+ (select count(*) from memory_chunks where user_id = $1) + (select count(*) from conversations where user_id = $1)
		+ (select count(*) from messages where user_id = $1) + (select count(*) from tags where user_id = $1)`, aliceID).Scan(&remaining)
	if remaining != 0 {
		t.Fatalf("%d rows remain after delete-everything", remaining)
	}
	if n := e.store.count("users/" + aliceID); n != 0 {
		t.Fatalf("%d files remain after delete-everything", n)
	}
	// Bob's data is untouched.
	var bobConvs int
	e.db.Pool.QueryRow(context.Background(), `select count(*) from conversations where user_id <> $1`, aliceID).Scan(&bobConvs)
	if bobConvs != 1 {
		t.Fatalf("bob's conversations = %d", bobConvs)
	}
}

func TestRLSDirect(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	a, b := uuid.NewString(), uuid.NewString()
	for _, id := range []string{a, b} {
		if _, err := e.db.Pool.Exec(ctx, `insert into auth.users (id) values ($1)`, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.db.Pool.Exec(ctx, `insert into memories (user_id, type, title) values ($1, 'note', 'secret')`, a); err != nil {
		t.Fatal(err)
	}
	var n int
	// Direct SQL as user b.
	tx, err := e.db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `select set_config('request.jwt.claims', $1, true), set_config('role','authenticated', true)`,
		fmt.Sprintf(`{"sub":"%s"}`, b)); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, `select count(*) from memories`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("user b sees %d memories (err %v)", n, err)
	}
	if _, err := tx.Exec(ctx, `insert into memories (user_id, type, title) values ($1, 'note', 'forged')`, a); err == nil {
		t.Fatal("user b inserted a memory owned by user a")
	}
}
