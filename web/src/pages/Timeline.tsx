import { useInfiniteQuery } from "@tanstack/react-query";
import { MemoryCard } from "../components/MemoryCard";
import { api } from "../lib/api";
import { SkeletonList } from "./Home";

export function Timeline() {
  const q = useInfiniteQuery({
    queryKey: ["timeline"],
    queryFn: ({ pageParam }) => api.timeline(pageParam || undefined),
    initialPageParam: "",
    getNextPageParam: (last) => last.next_cursor || undefined,
  });

  // Merge groups across pages (a month can span a page boundary).
  const groups: { month: string; label: string; memories: import("../lib/types").Memory[] }[] = [];
  for (const page of q.data?.pages ?? []) {
    for (const g of page.groups) {
      const last = groups[groups.length - 1];
      if (last && last.month === g.month) last.memories.push(...g.memories);
      else groups.push({ ...g, memories: [...g.memories] });
    }
  }

  return (
    <div className="space-y-6">
      <h1 className="text-xl font-semibold">Timeline</h1>
      {q.isLoading && <SkeletonList />}
      {q.isError && <p className="text-sm text-red-600">{(q.error as Error).message}</p>}
      {!q.isLoading && groups.length === 0 && <p className="text-zinc-500">Nothing saved yet.</p>}
      {groups.map((g) => (
        <section key={g.month} className="space-y-3">
          <h2 className="sticky top-14 z-10 bg-zinc-50/90 py-1 text-sm font-semibold uppercase tracking-wide text-zinc-500 backdrop-blur dark:bg-zinc-950/90">
            {g.label}
          </h2>
          <div className="space-y-2">
            {g.memories.map((m) => (
              <MemoryCard key={m.id} memory={m} compact />
            ))}
          </div>
        </section>
      ))}
      {q.hasNextPage && (
        <button className="btn-ghost w-full" onClick={() => q.fetchNextPage()} disabled={q.isFetchingNextPage}>
          {q.isFetchingNextPage ? "Loading…" : "Older memories"}
        </button>
      )}
    </div>
  );
}
