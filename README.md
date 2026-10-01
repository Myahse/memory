# Memory

**Save anything. Find anything.**

Memory is a private, searchable AI archive. Save screenshots, photos, receipts,
PDFs, documents, voice notes, text notes and links — Memory reads, transcribes,
summarises, tags and indexes them automatically, so weeks later you can simply
ask: *"What was that laptop I was looking at last month?"* and get the answer
with the source memories.

> Google Drive stores your files. **Memory understands them.**

| Part | Stack | Path |
|---|---|---|
| Mobile app | Flutter (iOS + Android), offline-first capture | [`mobile/`](mobile) |
| Web app | React + TypeScript + Vite + Tailwind | [`web/`](web) |
| Backend | Go: HTTP API + processing worker | [`backend/`](backend) |
| Data | Supabase: PostgreSQL + pgvector, Auth, private Storage, RLS, Realtime | [`supabase/`](supabase) |

See [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) for the full design.

```
 📱 Flutter / 🖥️ Web ── capture · compress · offline queue · display
          │  (Supabase Auth JWT)
          ▼
 ☁️ Go API ──────────────► Supabase Storage (private, signed URLs)
          │ processing_jobs
          ▼
 ⚙️ Go worker: OCR · vision · speech-to-text · extraction · summary · tags
          │            chunking · embeddings
          ▼
 🗄️ PostgreSQL + pgvector (RLS) ──► hybrid search (full-text + vector, RRF)
                                     │
                                     ▼
                              🤖 LLM (RAG) ──► answer + verified sources
```

## Quick start (local)

Prerequisites: [Supabase CLI](https://supabase.com/docs/guides/cli), Go 1.26+,
Node 22+, Flutter 3.38+ (Dart 3.10+).

### 1. Database, auth and storage

```bash
supabase init            # once; keeps the migrations in supabase/migrations
supabase start           # local Postgres + Auth + Storage + Realtime
supabase db reset        # applies supabase/migrations
supabase status          # prints API URL, anon key, service_role key, DB URL
```

For a hosted project: `supabase link --project-ref <ref> && supabase db push`.
Enable the Google and Apple providers under *Authentication → Providers* and
add `http://localhost:5173` and `app.memory://login-callback` to the redirect
URLs.

### 2. Backend

```bash
cd backend
cp .env.example .env     # fill in the values from `supabase status` + AI keys
set -a && . ./.env && set +a
go run ./cmd/memory      # API on :8080 and the worker in one process (MODE=all)
```

No AI keys yet? Set all `AI_*_PROVIDER=mock` to run the full pipeline offline
with a deterministic mock provider.

### 3. Web

```bash
cd web
cp .env.example .env     # VITE_SUPABASE_URL, VITE_SUPABASE_ANON_KEY, VITE_API_URL
npm install
npm run dev              # http://localhost:5173
```

### 4. Mobile

```bash
cd mobile
flutter pub get
flutter run \
  --dart-define=SUPABASE_URL=http://10.0.2.2:54321 \
  --dart-define=SUPABASE_ANON_KEY=<anon key> \
  --dart-define=API_URL=http://10.0.2.2:8080
```

(`10.0.2.2` is the host machine from the Android emulator; use your LAN IP on a
real device and `localhost` on the iOS simulator.)

## Tests

```bash
# Backend unit + end-to-end tests (real Postgres + pgvector, mock AI, fake storage)
cd backend
createdb memory_test
psql memory_test -f testdata/supabase_stub.sql
for f in ../supabase/migrations/*.sql; do psql memory_test -v ON_ERROR_STOP=1 -f "$f"; done
TEST_DATABASE_URL=postgres://localhost/memory_test go test ./...

cd ../web && npm test && npm run build
cd ../mobile && flutter analyze && flutter test
```

The end-to-end test covers: auth, profile creation, note/document/photo/voice
creation, direct uploads, asynchronous processing, keyword and semantic search,
filters, RAG answers with sources and the explicit "nothing found" answer,
duplicate detection, user edits surviving reprocessing, cross-user isolation
(through the API **and** with direct SQL under RLS), share links and
revocation, timeline, single-memory deletion and *Delete my data*.

## Configuration

All backend settings are environment variables — see
[`backend/.env.example`](backend/.env.example). Each AI capability (LLM,
vision, speech-to-text, embeddings) picks its provider independently
(`anthropic`, `openai` or any OpenAI-compatible endpoint, `mock`). Defaults:
Claude (`claude-opus-5-5`) for vision, understanding and answers; OpenAI for
transcription and 1536-dimension embeddings.

## Deployment

* **Database / Auth / Storage:** a Supabase project (`supabase db push`).
* **Backend:** `backend/Dockerfile` builds a static, distroless image. Run it
  as two services for scale — `MODE=api` (stateless, horizontally scalable) and
  `MODE=worker` (any number of replicas; jobs are claimed with
  `FOR UPDATE SKIP LOCKED`).
* **Web:** `npm run build` → static `web/dist` on any CDN (SPA fallback to
  `index.html`).
* **Mobile:** `flutter build ipa` / `flutter build appbundle` with the same
  `--dart-define` values.

## Plans

Limits are enforced server-side and configurable (`FREE_*`, `PRO_*`). Free:
1 GiB storage, 50 AI questions and 200 processed items per month, 25 MiB per
file. Pro: 50 GiB, unlimited questions and processing, 100 MiB per file.
Pricing is intentionally not hard-coded — test it.
