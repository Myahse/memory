import { useInfiniteQuery } from "@tanstack/react-query";
import { Plus, Search } from "lucide-react";
import { useState } from "react";
import { useNavigate, useOutletContext } from "react-router-dom";
import type { LayoutContext } from "../components/Layout";
import { MemoryCard } from "../components/MemoryCard";
import { api } from "../lib/api";

export function SearchBox({ initial = "", autoFocus = false }: { initial?: string; autoFocus?: boolean }) {
  const nav = useNavigate();
  const [q, setQ] = useState(initial);
  return (
    <form
      role="search"
      onSubmit={(e) => {
        e.preventDefault();
        if (q.trim()) nav(`/search?q=${encodeURIComponent(q.trim())}`);
      }}
      className="relative"
    >
      <Search className="pointer-events-none absolute left-4 top-1/2 size-5 -translate-y-1/2 text-zinc-400" />
      <input
        className="input rounded-2xl py-3.5 pl-12 text-base shadow-sm"
        placeholder="Ask your Memory…"
        value={q}
        onChange={(e) => setQ(e.target.value)}
        autoFocus={autoFocus}
        aria-label="Search your memories"
      />
    </form>
  );
}

export function Home() {
  const { openAdd } = useOutletContext<LayoutContext>();
  const query = useInfiniteQuery({
    queryKey: ["memories", "recent"],
    queryFn: ({ pageParam }) => api.listMemories({ before: pageParam || undefined, limit: 30 }),
    initialPageParam: "",
    getNextPageParam: (last) => last.next_cursor || undefined,
    // While something is processing, poll as a fallback to Realtime.
    refetchInterval: (q) =>
      q.state.data?.pages.some((p) => p.memories.some((m) => m.status === "pending" || m.status === "processing")) ? 5000 : false,
  });
  const memories = query.data?.pages.flatMap((p) => p.memories) ?? [];

  return (
    <div className="space-y-6">
      <SearchBox />
      <button onClick={openAdd} className="btn-primary w-full rounded-2xl py-3.5 text-base">
        <Plus className="size-5" /> Add Memory
      </button>

      <section className="space-y-3">
        <h2 className="text-sm font-semibold uppercase tracking-wide text-zinc-500">Recent</h2>
        {query.isLoading && <SkeletonList />}
        {query.isError && (
          <div className="card p-4 text-sm text-red-600">
            {(query.error as Error).message}{" "}
            <button className="underline" onClick={() => query.refetch()}>
              Retry
            </button>
          </div>
        )}
        {!query.isLoading && memories.length === 0 && !query.isError && (
          <div className="card space-y-2 p-8 text-center">
            <p className="text-lg font-medium">Your Memory is empty</p>
            <p className="text-sm text-zinc-500">Save a screenshot, a receipt, a note or a voice memo — then just ask for it later.</p>
          </div>
        )}
        <div className="grid gap-3 sm:grid-cols-2">
          {memories.map((m) => (
            <MemoryCard key={m.id} memory={m} />
          ))}
        </div>
        {query.hasNextPage && (
          <button className="btn-ghost w-full" onClick={() => query.fetchNextPage()} disabled={query.isFetchingNextPage}>
            {query.isFetchingNextPage ? "Loading…" : "Load more"}
          </button>
        )}
      </section>
    </div>
  );
}

export function SkeletonList({ n = 4 }: { n?: number }) {
  return (
    <div className="grid gap-3 sm:grid-cols-2">
      {Array.from({ length: n }, (_, i) => (
        <div key={i} className="card h-20 animate-pulse bg-zinc-100 dark:bg-zinc-900" />
      ))}
    </div>
  );
}
