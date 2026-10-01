-- Memory: initial schema
-- Private, per-user AI archive. Every user-owned row carries user_id and is
-- protected by Row Level Security. Embeddings are 1536-dimensional
-- (matches the default embedding model; see backend/internal/ai).

create extension if not exists vector with schema extensions;
create extension if not exists pg_trgm with schema extensions;

-- ---------------------------------------------------------------------------
-- Helpers
-- ---------------------------------------------------------------------------

create or replace function public.set_updated_at()
returns trigger
language plpgsql
as $$
begin
  new.updated_at = now();
  return new;
end;
$$;

-- ---------------------------------------------------------------------------
-- Enums
-- ---------------------------------------------------------------------------

create type public.memory_type as enum (
  'photo', 'screenshot', 'pdf', 'document', 'receipt', 'voice', 'note', 'link'
);

create type public.memory_status as enum ('pending', 'processing', 'ready', 'failed');

create type public.job_status as enum ('queued', 'running', 'succeeded', 'failed');

create type public.message_role as enum ('user', 'assistant');

create type public.plan_tier as enum ('free', 'pro');

-- ---------------------------------------------------------------------------
-- profiles
-- ---------------------------------------------------------------------------

create table public.profiles (
  id          uuid primary key default gen_random_uuid(),
  user_id     uuid not null unique references auth.users (id) on delete cascade,
  name        text,
  avatar_url  text,
  plan        public.plan_tier not null default 'free',
  created_at  timestamptz not null default now(),
  updated_at  timestamptz not null default now()
);

create trigger profiles_updated_at before update on public.profiles
  for each row execute function public.set_updated_at();

-- Create a profile automatically for every new auth user.
create or replace function public.handle_new_user()
returns trigger
language plpgsql
security definer
set search_path = public
as $$
begin
  insert into public.profiles (user_id, name, avatar_url)
  values (
    new.id,
    coalesce(new.raw_user_meta_data ->> 'name', new.raw_user_meta_data ->> 'full_name'),
    new.raw_user_meta_data ->> 'avatar_url'
  )
  on conflict (user_id) do nothing;
  return new;
end;
$$;

create trigger on_auth_user_created
  after insert on auth.users
  for each row execute function public.handle_new_user();

-- ---------------------------------------------------------------------------
-- memories
-- ---------------------------------------------------------------------------

create table public.memories (
  id              uuid primary key default gen_random_uuid(),
  user_id         uuid not null references auth.users (id) on delete cascade,
  type            public.memory_type not null,
  status          public.memory_status not null default 'pending',
  title           text,
  content         text,              -- extracted / written text
  summary         text,
  category        text,
  file_path       text,              -- users/{user_id}/memories/{id}/original
  thumbnail_path  text,              -- users/{user_id}/memories/{id}/thumbnail
  source_url      text,
  mime_type       text,
  file_size       bigint not null default 0 check (file_size >= 0),
  content_hash    text,              -- sha256 of the original bytes / normalized text
  client_id       text,              -- idempotency key from offline clients
  captured_at     timestamptz,       -- when the content was created (EXIF, doc date…)
  metadata        jsonb not null default '{}'::jsonb,
  user_edited     text[] not null default '{}', -- fields the user changed; AI never overwrites them
  processing_error text,
  embedding       extensions.vector(1536),
  search_tsv      tsvector generated always as (
    setweight(to_tsvector('simple', coalesce(title, '')), 'A') ||
    setweight(to_tsvector('english', coalesce(title, '')), 'A') ||
    setweight(to_tsvector('simple', coalesce(category, '')), 'B') ||
    setweight(to_tsvector('english', coalesce(summary, '')), 'B') ||
    setweight(to_tsvector('simple', coalesce(content, '')), 'C') ||
    setweight(to_tsvector('english', coalesce(content, '')), 'C') ||
    setweight(to_tsvector('simple', coalesce(source_url, '')), 'D')
  ) stored,
  created_at      timestamptz not null default now(),
  updated_at      timestamptz not null default now(),
  constraint memories_client_id_unique unique (user_id, client_id),
  -- Files must live under the owner's folder.
  constraint memories_file_path_owner check (
    file_path is null or file_path like 'users/' || user_id::text || '/memories/' || id::text || '/%'
  ),
  constraint memories_thumbnail_path_owner check (
    thumbnail_path is null or thumbnail_path like 'users/' || user_id::text || '/memories/' || id::text || '/%'
  )
);

create index memories_user_created_idx on public.memories (user_id, created_at desc);
create index memories_user_type_idx on public.memories (user_id, type);
create index memories_user_category_idx on public.memories (user_id, category);
create index memories_user_hash_idx on public.memories (user_id, content_hash);
create index memories_search_tsv_idx on public.memories using gin (search_tsv);
create index memories_title_trgm_idx on public.memories using gin (title extensions.gin_trgm_ops);
create index memories_embedding_idx on public.memories
  using hnsw (embedding extensions.vector_cosine_ops);

create trigger memories_updated_at before update on public.memories
  for each row execute function public.set_updated_at();

-- ---------------------------------------------------------------------------
-- memory_chunks
-- ---------------------------------------------------------------------------

create table public.memory_chunks (
  id           uuid primary key default gen_random_uuid(),
  memory_id    uuid not null references public.memories (id) on delete cascade,
  user_id      uuid not null references auth.users (id) on delete cascade,
  content      text not null,
  chunk_index  int not null,
  embedding    extensions.vector(1536),
  content_tsv  tsvector generated always as (
    to_tsvector('simple', content) || to_tsvector('english', content)
  ) stored,
  created_at   timestamptz not null default now(),
  unique (memory_id, chunk_index)
);

create index memory_chunks_user_idx on public.memory_chunks (user_id);
create index memory_chunks_memory_idx on public.memory_chunks (memory_id);
create index memory_chunks_tsv_idx on public.memory_chunks using gin (content_tsv);
create index memory_chunks_embedding_idx on public.memory_chunks
  using hnsw (embedding extensions.vector_cosine_ops);

-- ---------------------------------------------------------------------------
-- tags
-- ---------------------------------------------------------------------------

create table public.tags (
  id          uuid primary key default gen_random_uuid(),
  user_id     uuid not null references auth.users (id) on delete cascade,
  name        text not null check (char_length(name) between 1 and 64),
  created_at  timestamptz not null default now()
);

create unique index tags_user_name_idx on public.tags (user_id, lower(name));

create table public.memory_tags (
  id         uuid primary key default gen_random_uuid(),
  memory_id  uuid not null references public.memories (id) on delete cascade,
  tag_id     uuid not null references public.tags (id) on delete cascade,
  user_id    uuid not null references auth.users (id) on delete cascade,
  source     text not null default 'ai' check (source in ('ai', 'user')),
  unique (memory_id, tag_id)
);

create index memory_tags_tag_idx on public.memory_tags (tag_id);
create index memory_tags_user_idx on public.memory_tags (user_id);

-- ---------------------------------------------------------------------------
-- conversations / messages
-- ---------------------------------------------------------------------------

create table public.conversations (
  id          uuid primary key default gen_random_uuid(),
  user_id     uuid not null references auth.users (id) on delete cascade,
  title       text,
  created_at  timestamptz not null default now(),
  updated_at  timestamptz not null default now()
);

create index conversations_user_idx on public.conversations (user_id, updated_at desc);

create trigger conversations_updated_at before update on public.conversations
  for each row execute function public.set_updated_at();

create table public.messages (
  id               uuid primary key default gen_random_uuid(),
  conversation_id  uuid not null references public.conversations (id) on delete cascade,
  user_id          uuid not null references auth.users (id) on delete cascade,
  role             public.message_role not null,
  content          text not null,
  -- Memory ids actually retrieved and cited for this answer.
  source_memory_ids uuid[] not null default '{}',
  created_at       timestamptz not null default now()
);

create index messages_conversation_idx on public.messages (conversation_id, created_at);

-- ---------------------------------------------------------------------------
-- processing_jobs
-- ---------------------------------------------------------------------------

create table public.processing_jobs (
  id            uuid primary key default gen_random_uuid(),
  memory_id     uuid not null references public.memories (id) on delete cascade,
  user_id       uuid not null references auth.users (id) on delete cascade,
  job_type      text not null default 'process' check (job_type in ('process', 'reembed')),
  status        public.job_status not null default 'queued',
  attempts      int not null default 0,
  error         text,
  run_after     timestamptz not null default now(),
  locked_until  timestamptz,
  created_at    timestamptz not null default now(),
  started_at    timestamptz,
  completed_at  timestamptz
);

create index processing_jobs_queue_idx on public.processing_jobs (status, run_after)
  where status in ('queued', 'running');
create index processing_jobs_memory_idx on public.processing_jobs (memory_id);

-- ---------------------------------------------------------------------------
-- share_links (single-memory, revocable, expiring)
-- ---------------------------------------------------------------------------

create table public.share_links (
  id           uuid primary key default gen_random_uuid(),
  user_id      uuid not null references auth.users (id) on delete cascade,
  memory_id    uuid not null references public.memories (id) on delete cascade,
  token_hash   text not null unique,     -- sha256 of the token; the token itself is never stored
  expires_at   timestamptz not null,
  revoked_at   timestamptz,
  created_at   timestamptz not null default now()
);

create index share_links_memory_idx on public.share_links (memory_id);

-- ---------------------------------------------------------------------------
-- usage_counters (plan limits)
-- ---------------------------------------------------------------------------

create table public.usage_counters (
  user_id          uuid not null references auth.users (id) on delete cascade,
  period           date not null,          -- first day of the month (UTC)
  ai_queries       int not null default 0,
  processed_items  int not null default 0,
  primary key (user_id, period)
);

-- ---------------------------------------------------------------------------
-- Row Level Security
-- ---------------------------------------------------------------------------

alter table public.profiles        enable row level security;
alter table public.memories        enable row level security;
alter table public.memory_chunks   enable row level security;
alter table public.tags            enable row level security;
alter table public.memory_tags     enable row level security;
alter table public.conversations   enable row level security;
alter table public.messages        enable row level security;
alter table public.processing_jobs enable row level security;
alter table public.share_links     enable row level security;
alter table public.usage_counters  enable row level security;

-- profiles: own row only; plan can only be changed by the service role.
create policy "profiles_select_own" on public.profiles
  for select to authenticated using (user_id = (select auth.uid()));
create policy "profiles_update_own" on public.profiles
  for update to authenticated
  using (user_id = (select auth.uid()))
  with check (user_id = (select auth.uid()));
revoke insert, update on public.profiles from authenticated;
grant update (name, avatar_url) on public.profiles to authenticated;

-- memories: full CRUD on own rows.
create policy "memories_select_own" on public.memories
  for select to authenticated using (user_id = (select auth.uid()));
create policy "memories_insert_own" on public.memories
  for insert to authenticated with check (user_id = (select auth.uid()));
create policy "memories_update_own" on public.memories
  for update to authenticated
  using (user_id = (select auth.uid()))
  with check (user_id = (select auth.uid()));
create policy "memories_delete_own" on public.memories
  for delete to authenticated using (user_id = (select auth.uid()));

-- Column-level privileges: clients may only write user-editable fields.
-- Processing state, file paths/sizes, hashes and embeddings are written by the
-- backend (service connection) so quotas and the pipeline cannot be bypassed.
revoke insert, update on public.memories from authenticated;
grant insert (id, user_id, type, title, content, source_url, mime_type, file_size,
              content_hash, client_id, captured_at, metadata)
  on public.memories to authenticated;
grant update (title, summary, content, category, captured_at, type, user_edited)
  on public.memories to authenticated;
-- file_size is declared at creation and verified by the backend after upload.
alter table public.memories add constraint memories_file_size_declared check (file_size <= 104857600);

-- memory_chunks: read/delete own; writes happen in the backend worker.
create policy "memory_chunks_select_own" on public.memory_chunks
  for select to authenticated using (user_id = (select auth.uid()));
create policy "memory_chunks_delete_own" on public.memory_chunks
  for delete to authenticated using (user_id = (select auth.uid()));

-- tags
create policy "tags_select_own" on public.tags
  for select to authenticated using (user_id = (select auth.uid()));
create policy "tags_insert_own" on public.tags
  for insert to authenticated with check (user_id = (select auth.uid()));
create policy "tags_update_own" on public.tags
  for update to authenticated
  using (user_id = (select auth.uid())) with check (user_id = (select auth.uid()));
create policy "tags_delete_own" on public.tags
  for delete to authenticated using (user_id = (select auth.uid()));

-- memory_tags: both sides must belong to the caller.
create policy "memory_tags_select_own" on public.memory_tags
  for select to authenticated using (user_id = (select auth.uid()));
create policy "memory_tags_insert_own" on public.memory_tags
  for insert to authenticated with check (
    user_id = (select auth.uid())
    and exists (select 1 from public.memories m where m.id = memory_id and m.user_id = (select auth.uid()))
    and exists (select 1 from public.tags t where t.id = tag_id and t.user_id = (select auth.uid()))
  );
create policy "memory_tags_delete_own" on public.memory_tags
  for delete to authenticated using (user_id = (select auth.uid()));

-- conversations / messages
create policy "conversations_all_own" on public.conversations
  for all to authenticated
  using (user_id = (select auth.uid())) with check (user_id = (select auth.uid()));
create policy "messages_select_own" on public.messages
  for select to authenticated using (user_id = (select auth.uid()));
create policy "messages_insert_own" on public.messages
  for insert to authenticated with check (
    user_id = (select auth.uid())
    and exists (select 1 from public.conversations c
                where c.id = conversation_id and c.user_id = (select auth.uid()))
  );
create policy "messages_delete_own" on public.messages
  for delete to authenticated using (user_id = (select auth.uid()));

-- processing_jobs: users can see the status of their own jobs.
create policy "processing_jobs_select_own" on public.processing_jobs
  for select to authenticated using (user_id = (select auth.uid()));

-- share_links
create policy "share_links_select_own" on public.share_links
  for select to authenticated using (user_id = (select auth.uid()));
create policy "share_links_insert_own" on public.share_links
  for insert to authenticated with check (
    user_id = (select auth.uid())
    and exists (select 1 from public.memories m where m.id = memory_id and m.user_id = (select auth.uid()))
  );
create policy "share_links_update_own" on public.share_links
  for update to authenticated
  using (user_id = (select auth.uid())) with check (user_id = (select auth.uid()));
create policy "share_links_delete_own" on public.share_links
  for delete to authenticated using (user_id = (select auth.uid()));

-- usage_counters: read-only for the owner; written by the backend.
create policy "usage_counters_select_own" on public.usage_counters
  for select to authenticated using (user_id = (select auth.uid()));

-- Anonymous users get nothing.
revoke all on all tables in schema public from anon;

-- ---------------------------------------------------------------------------
-- Search functions (SECURITY INVOKER: RLS applies to every row they touch)
-- ---------------------------------------------------------------------------

-- Hybrid retrieval: full-text (memories + chunks) and vector (memory + chunk
-- embeddings) combined with Reciprocal Rank Fusion, then metadata filters.
create or replace function public.search_memories(
  query_text        text,
  query_embedding   extensions.vector(1536) default null,
  match_count       int default 20,
  filter_types      public.memory_type[] default null,
  filter_category   text default null,
  filter_tags       text[] default null,
  filter_from       timestamptz default null,
  filter_to         timestamptz default null,
  min_similarity    float default 0.25
)
returns table (
  id               uuid,
  rrf_score        float,
  keyword_rank     int,
  vector_rank      int,
  similarity       float,
  snippet          text
)
language sql
stable
security invoker
set search_path = public, extensions
as $$
  with params as (
    select
      websearch_to_tsquery('english', coalesce(query_text, '')) as q_en,
      websearch_to_tsquery('simple',  coalesce(query_text, '')) as q_simple,
      greatest(least(match_count, 100), 1) as k
  ),
  candidates as (
    select m.*
    from memories m
    where m.user_id = auth.uid()
      and (filter_types is null or m.type = any (filter_types))
      and (filter_category is null or lower(m.category) = lower(filter_category))
      and (filter_from is null or coalesce(m.captured_at, m.created_at) >= filter_from)
      and (filter_to   is null or coalesce(m.captured_at, m.created_at) <  filter_to)
      and (filter_tags is null or exists (
            select 1 from memory_tags mt join tags t on t.id = mt.tag_id
            where mt.memory_id = m.id and lower(t.name) = any (
              select lower(x) from unnest(filter_tags) x)))
  ),
  keyword as (
    select c.id,
           row_number() over (order by
             greatest(ts_rank_cd(c.search_tsv, p.q_en), ts_rank_cd(c.search_tsv, p.q_simple))
             + coalesce(max(ts_rank_cd(ch.content_tsv, p.q_en)), 0)
             + similarity(coalesce(c.title, ''), coalesce(query_text, '')) desc) as rank
    from candidates c
    cross join params p
    left join memory_chunks ch
      on ch.memory_id = c.id and (ch.content_tsv @@ p.q_en or ch.content_tsv @@ p.q_simple)
    where c.search_tsv @@ p.q_en
       or c.search_tsv @@ p.q_simple
       or ch.id is not null
       or similarity(coalesce(c.title, ''), coalesce(query_text, '')) > 0.3
    group by c.id, c.search_tsv, c.title, p.q_en, p.q_simple
    order by rank
    limit (select k * 2 from params)
  ),
  vec_raw as (
    select c.id, 1 - (c.embedding <=> query_embedding) as sim
    from candidates c
    where query_embedding is not null and c.embedding is not null
    union all
    select ch.memory_id, 1 - (ch.embedding <=> query_embedding)
    from memory_chunks ch
    join candidates c on c.id = ch.memory_id
    where query_embedding is not null and ch.embedding is not null
  ),
  vec as (
    select v.id, max(v.sim) as sim,
           row_number() over (order by max(v.sim) desc) as rank
    from vec_raw v
    group by v.id
    having max(v.sim) >= min_similarity
    order by rank
    limit (select k * 2 from params)
  ),
  fused as (
    select coalesce(k.id, v.id) as id,
           coalesce(1.0 / (60 + k.rank), 0) + coalesce(1.0 / (60 + v.rank), 0) as rrf,
           k.rank::int as krank,
           v.rank::int as vrank,
           v.sim
    from keyword k
    full outer join vec v on v.id = k.id
  )
  select f.id,
         f.rrf::float,
         f.krank,
         f.vrank,
         f.sim::float,
         (select ts_headline('english', ch.content,
                   (select q_en from params),
                   'MaxFragments=1, MaxWords=30, MinWords=10, StartSel="", StopSel=""')
          from memory_chunks ch
          where ch.memory_id = f.id
          order by ts_rank_cd(ch.content_tsv, (select q_en from params)) desc,
                   case when query_embedding is null or ch.embedding is null then 1
                        else ch.embedding <=> query_embedding end asc
          limit 1) as snippet
  from fused f
  order by f.rrf desc
  limit (select k from params);
$$;

-- Semantically similar memories to a given memory.
create or replace function public.related_memories(
  target_memory_id uuid,
  match_count      int default 6,
  min_similarity   float default 0.35
)
returns table (id uuid, similarity float)
language sql
stable
security invoker
set search_path = public, extensions
as $$
  select m.id, (1 - (m.embedding <=> t.embedding))::float as similarity
  from memories t
  join memories m on m.user_id = t.user_id and m.id <> t.id
  where t.id = target_memory_id
    and t.user_id = auth.uid()
    and t.embedding is not null
    and m.embedding is not null
    and 1 - (m.embedding <=> t.embedding) >= min_similarity
  order by m.embedding <=> t.embedding
  limit greatest(least(match_count, 50), 1);
$$;

grant execute on function public.search_memories to authenticated;
grant execute on function public.related_memories to authenticated;
revoke execute on function public.search_memories from anon;
revoke execute on function public.related_memories from anon;

-- ---------------------------------------------------------------------------
-- Realtime: clients subscribe to status changes on their own memories.
-- ---------------------------------------------------------------------------

do $$
begin
  if exists (select 1 from pg_publication where pubname = 'supabase_realtime') then
    alter publication supabase_realtime add table public.memories;
  end if;
end;
$$;
