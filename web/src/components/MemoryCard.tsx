import { AlertCircle, Loader2 } from "lucide-react";
import { Link } from "react-router-dom";
import { timeAgo } from "../lib/format";
import { TYPE_META, type Memory } from "../lib/types";

export function StatusBadge({ memory }: { memory: Memory }) {
  if (memory.status === "ready") return null;
  if (memory.status === "failed")
    return (
      <span className="inline-flex items-center gap-1 text-xs font-medium text-red-600 dark:text-red-400">
        <AlertCircle className="size-3.5" /> Processing failed
      </span>
    );
  return (
    <span className="inline-flex items-center gap-1 text-xs font-medium text-accent">
      <Loader2 className="size-3.5 animate-spin" /> Processing memory…
    </span>
  );
}

export function displayTitle(m: Memory) {
  if (m.title) return m.title;
  if (m.type === "link" && m.source_url) return m.source_url;
  if (m.type === "note" && m.content) return m.content.slice(0, 80);
  const name = (m.metadata?.original_name as string | undefined) ?? "";
  return name || TYPE_META[m.type].label;
}

export function MemoryCard({ memory, snippet, compact }: { memory: Memory; snippet?: string; compact?: boolean }) {
  const meta = TYPE_META[memory.type];
  return (
    <Link
      to={`/memory/${memory.id}`}
      className="card group flex gap-3 p-3 transition hover:border-accent/50 hover:shadow-md focus:outline-none focus-visible:ring-2 focus-visible:ring-accent"
    >
      <div className="flex size-14 shrink-0 items-center justify-center overflow-hidden rounded-xl bg-zinc-100 text-2xl dark:bg-zinc-800">
        {memory.thumbnail_url ? (
          <img src={memory.thumbnail_url} alt="" className="size-full object-cover" loading="lazy" />
        ) : (
          <span aria-hidden>{meta.emoji}</span>
        )}
      </div>
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2 text-xs text-zinc-500 dark:text-zinc-400">
          <span>
            {meta.emoji} {meta.label}
          </span>
          <span aria-hidden>·</span>
          <time dateTime={memory.created_at}>{timeAgo(memory.created_at)}</time>
        </div>
        <div className="truncate font-medium">{displayTitle(memory)}</div>
        {!compact && (snippet || memory.summary) && (
          <p className="line-clamp-2 text-sm text-zinc-600 dark:text-zinc-400">{snippet || memory.summary}</p>
        )}
        <StatusBadge memory={memory} />
      </div>
    </Link>
  );
}
