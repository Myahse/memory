import { useQuery } from "@tanstack/react-query";
import { ArrowRight } from "lucide-react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { MemoryCard } from "../components/MemoryCard";
import { api } from "../lib/api";
import { TYPE_META, type MemoryType } from "../lib/types";
import { SearchBox, SkeletonList } from "./Home";

const FILTERS: { label: string; types: MemoryType[] }[] = [
  { label: "All", types: [] },
  { label: "Documents", types: ["pdf", "document"] },
  { label: "Screenshots", types: ["screenshot"] },
  { label: "Photos", types: ["photo"] },
  { label: "Receipts", types: ["receipt"] },
  { label: "Voice", types: ["voice"] },
  { label: "Notes", types: ["note"] },
  { label: "Links", types: ["link"] },
];

const RANGES: { label: string; days: number }[] = [
  { label: "Any time", days: 0 },
  { label: "Past week", days: 7 },
  { label: "Past month", days: 31 },
  { label: "Past year", days: 365 },
];

export function SearchPage() {
  const [params, setParams] = useSearchParams();
  const nav = useNavigate();
  const q = params.get("q") ?? "";
  const filter = params.get("f") ?? "All";
  const days = Number(params.get("d") ?? 0);
  const category = params.get("c") ?? "";
  const types = FILTERS.find((f) => f.label === filter)?.types ?? [];
  const from = days ? new Date(Date.now() - days * 86_400_000).toISOString() : undefined;

  const facets = useQuery({ queryKey: ["facets"], queryFn: api.facets, staleTime: 60_000 });
  const results = useQuery({
    queryKey: ["search", q, filter, days, category],
    queryFn: () => api.search(q, { types: types.length ? types : undefined, from, category: category || undefined }),
    enabled: q.length > 0,
  });

  const set = (k: string, v: string) => {
    const next = new URLSearchParams(params);
    if (v) next.set(k, v);
    else next.delete(k);
    setParams(next, { replace: true });
  };

  const hits = results.data?.results ?? [];
  return (
    <div className="space-y-4">
      <SearchBox initial={q} key={q} />

      <div className="-mx-4 flex gap-2 overflow-x-auto px-4 pb-1">
        {FILTERS.map((f) => (
          <button
            key={f.label}
            onClick={() => set("f", f.label === "All" ? "" : f.label)}
            className={`chip shrink-0 ${
              filter === f.label ? "border-accent bg-accent text-white" : "border-zinc-300 hover:border-accent dark:border-zinc-700"
            }`}
          >
            {f.label}
          </button>
        ))}
      </div>
      <div className="flex flex-wrap gap-2">
        <select className="input w-auto py-1.5" value={days} onChange={(e) => set("d", e.target.value === "0" ? "" : e.target.value)} aria-label="Date">
          {RANGES.map((r) => (
            <option key={r.days} value={r.days}>
              {r.label}
            </option>
          ))}
        </select>
        <select className="input w-auto py-1.5" value={category} onChange={(e) => set("c", e.target.value)} aria-label="Category">
          <option value="">All categories</option>
          {facets.data?.categories.map((c) => (
            <option key={c.name} value={c.name}>
              {c.name} ({c.count})
            </option>
          ))}
        </select>
      </div>

      {results.isLoading && <SkeletonList />}
      {results.isError && <p className="text-sm text-red-600">{(results.error as Error).message}</p>}
      {results.data && (
        <>
          <p className="text-sm text-zinc-500">
            {hits.length === 0 ? "No matching memories." : `${hits.length} relevant ${hits.length === 1 ? "memory" : "memories"}`}
          </p>
          <div className="space-y-3">
            {hits.map((h) => (
              <div key={h.memory.id}>
                <MemoryCard memory={h.memory} snippet={h.snippet} />
              </div>
            ))}
          </div>
          {hits.length > 0 && (
            <button
              className="btn-primary w-full"
              onClick={() =>
                nav("/ask", { state: { question: q, memoryIds: hits.slice(0, 8).map((h) => h.memory.id) } })
              }
            >
              Ask Memory about these results <ArrowRight className="size-4" />
            </button>
          )}
        </>
      )}
      {!q && (
        <div className="flex flex-wrap gap-2 text-sm">
          {facets.data?.tags.slice(0, 20).map((t) => (
            <button key={t.name} className="chip border-zinc-300 dark:border-zinc-700" onClick={() => set("q", t.name)}>
              #{t.name}
            </button>
          ))}
          {facets.data?.types.map((t) => (
            <span key={t.name} className="chip border-transparent bg-zinc-100 dark:bg-zinc-800">
              {TYPE_META[t.name as MemoryType]?.emoji} {t.count}
            </span>
          ))}
        </div>
      )}
    </div>
  );
}
