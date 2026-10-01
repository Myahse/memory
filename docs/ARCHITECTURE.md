# Memory — Architecture

## Principles

1. **Cloud-first intelligence, phone-first capture.** The phone captures,
   compresses (JPEG, max 2048 px), queues offline, uploads and displays. All
   OCR, vision, transcription, extraction, embeddings, retrieval and answer
   generation run in the cloud. No on-device models in the MVP.
2. **Never block the user on AI.** Saving returns immediately; processing is
   asynchronous and the UI updates live (Supabase Realtime, with polling as a
   fallback): *Processing memory… → ✓ Memory ready*, or *Processing failed* +
   Retry.
3. **Private by default, enforced in the database.** Every user-owned row has
   `user_id` and Row Level Security. The API runs each request's queries as the
   `authenticated` role with the caller's JWT claims, so RLS applies to the API
   exactly as it would to direct Supabase access. Clients never get public URLs.
4. **No invented memories.** The LLM only sees retrieved memories; its
   citations are validated against that set; if nothing relevant is retrieved
   the answer is a fixed *"I couldn't find anything relevant in your Memory
   archive."* and no LLM call is made.
5. **Swappable AI.** `backend/internal/ai` defines `LLM`, `Speech` and
   `Embedder` interfaces; providers are chosen per capability via env vars.

## Components

```
backend/
  cmd/memory            entrypoint (MODE=api|worker|all)
  internal/api          HTTP handlers, auth middleware, rate limiting, CORS
  internal/auth         Supabase JWT verification (HS256 secret or JWKS ES256/RS256)
  internal/db           pgx pool; WithUser (RLS) / WithService (worker) transactions
  internal/memory       memory model + SQL
  internal/pipeline     job queue worker: extract → analyse → chunk → embed → store
  internal/extract      PDF / DOCX / HTML / RTF / text extraction, chunking,
                        thumbnails, SSRF-safe link fetching
  internal/ai           provider interfaces, prompts, Anthropic / OpenAI / mock
  internal/search       hybrid retrieval
  internal/rag          question → query analysis → retrieval → grounded answer
  internal/quota        plan limits (storage, AI questions, processing)
supabase/migrations     schema, RLS, search SQL functions, storage bucket + policies
web/                    React app
mobile/                 Flutter app
```

## Data model

`profiles`, `memories`, `memory_chunks`, `tags`, `memory_tags`,
`conversations`, `messages`, `processing_jobs`, plus `share_links` and
`usage_counters`. Highlights:

* `memories.embedding vector(1536)` — whole-memory embedding (title, summary,
  category, tags, beginning of content) used for related memories and
  near-duplicate detection.
* `memory_chunks.embedding vector(1536)` — chunk embeddings (≈1200 chars,
  200 overlap, title-prefixed) for precise retrieval in long documents.
* `search_tsv` / `content_tsv` — generated full-text vectors (`simple` +
  `english` configurations, so names like *Carrefour* and stemmed words both
  match). HNSW indexes on embeddings, GIN on text, trigram on titles.
* `user_edited text[]` — fields the user changed; reprocessing never
  overwrites them.
* `client_id` — idempotency key from offline clients (unique per user).
* `content_hash` — SHA-256 of the original bytes / normalised text for exact
  duplicate detection; near-duplicates are flagged after embedding
  (`metadata.possible_duplicate_of`, cosine ≥ 0.97). Nothing is ever deleted
  automatically: the user chooses *Keep both*, *Replace* or *Cancel*.

## Storage

Private bucket `memories`:

```
users/{user_id}/memories/{memory_id}/original
users/{user_id}/memories/{memory_id}/thumbnail
```

Clients upload with a **one-time signed upload URL** issued by the API after
quota checks (there is no INSERT policy on `storage.objects`), and download via
short-lived signed URLs. A table constraint guarantees `file_path` always lives
under the owner's folder.

## Write path

```
client: POST /v1/memories {type, mime, size, sha256, client_id}
          ├─ 409 duplicate → user decides (keep_both | replace | cancel)
          └─ 201 {memory, upload:{url}}
client: PUT file → signed upload URL (direct to Storage)
client: POST /v1/memories/{id}/upload-complete → API verifies object + quota → job queued
worker: claim job (FOR UPDATE SKIP LOCKED, 10 min lease)
          photo/screenshot/receipt → thumbnail + one multimodal call (description,
                                     verbatim OCR, kind, entities, dates, receipt)
          pdf      → text layer; scanned PDFs are OCR'd by the vision model
          document → DOCX / TXT / MD / CSV / HTML / RTF extraction
          voice    → speech-to-text
          note     → as written
          link     → SSRF-safe fetch: title, description, og:image, readable text
        → analysis (title, summary, category, tags, entities, dates; relative
          dates resolved) → chunk → embed (batched) → one transaction:
          update memory, replace chunks, set tags, job succeeded, usage += 1
        → status = ready  (Realtime → "✓ Memory ready")
failure → retryable errors (timeouts, 429, 5xx, network) back off 30s·2ⁿ up to
          MAX_JOB_ATTEMPTS; permanent errors (unsupported/corrupt file, no
          speech, …) fail immediately with a user-facing message + Retry.
```

Notes and links skip the upload step. Edits to title/summary/content/tags
enqueue a cheap `reembed` job.

## Read path: hybrid retrieval + RAG

`search_memories()` (SQL, `SECURITY INVOKER` so RLS applies):

1. Candidate set = the caller's memories after metadata filters (type,
   category, tags, date range on `captured_at`/`created_at`).
2. Keyword ranking: `ts_rank_cd` over memory + chunk text (`english` and
   `simple`), plus trigram title similarity.
3. Vector ranking: best cosine similarity across the memory embedding and its
   chunk embeddings (minimum similarity threshold).
4. Reciprocal Rank Fusion (k = 60) → top N with a snippet.

`POST /v1/ask`:

```
question (+ previous question for follow-ups)
  → query analysis (LLM): rewritten query, keywords, type/date/category filters
  → hybrid search (filters relaxed once if they return nothing)
  → 0 results → fixed "couldn't find" answer, no LLM call
  → top 8 memories as numbered context (type, date, title, summary, tags, excerpt)
  → LLM returns {found, answer, sources[]}
  → sources ∩ retrieved set; empty → "couldn't find"
  → persisted in conversations/messages with source_memory_ids
```

"Ask Memory about these results" passes the result ids, so the answer is scoped
to exactly what the user saw.

## Offline (mobile)

Every capture is first written to the app's documents directory and an index
file (`memory_queue/queue.json`); the UI returns immediately. A sync loop runs
on start, on connectivity changes and every 30 s:
create (idempotent by `client_id`) → upload → complete → remove local copy.
States: *Waiting to sync*, *Uploading…*, *possible duplicate* (Keep both /
Replace / Cancel), *failed* (Retry / Discard). Recent memories are cached for
offline viewing.

## Security checklist

* RLS on every table; column-level privileges so clients can only write
  user-editable fields (processing state, file paths/sizes, hashes,
  embeddings, plan and usage are backend-only).
* API queries run as `authenticated` with the caller's claims; privileged
  connections (worker, deletion, share lookup) always filter by `user_id`.
* JWT verification: audience, expiry, role; JWKS cached and refreshed.
* Private bucket, signed upload/download URLs, no public memory URLs.
* Share links: 256-bit random tokens, only the SHA-256 is stored, expiry
  ≤ 30 days, revocable, expose a single memory without internal metadata.
* Link fetching blocks private / loopback / link-local / CGNAT addresses
  after DNS resolution and limits redirects and size.
* Per-user rate limits on create / search / ask; request size limits; no
  query strings or bodies in logs.
* *Delete my data* (`DELETE /v1/me/data`, typed confirmation): removes every
  stored object under `users/{id}/`, all rows (memories, chunks/embeddings,
  tags, conversations, messages, jobs, shares, usage) and optionally the auth
  account. Irreversible by design.

## Designed for later

* **Smart reminders / daily summary:** `metadata.dates` already holds resolved
  dates; add a scheduler worker mode.
* **Location memories:** add `location geography` + EXIF extraction; the
  search function already composes metadata filters.
* **People/entity connections:** promote `metadata.entities` to an
  `entities` + `memory_entities` table.
* **Collections:** `collections` + `memory_collections`; tags already exist.
* **Cross-memory reasoning / voice conversation:** the RAG engine is a
  separate package; add tool use and a speech front-end.
* **Browser extension / desktop:** more clients of the same API
  (`POST /v1/memories` with `type=link` or a file upload).
* **On-device AI:** the provider interfaces allow a hybrid where the phone does
  basic OCR / classification and the cloud does reasoning.
