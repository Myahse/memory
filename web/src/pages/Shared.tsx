import { useQuery } from "@tanstack/react-query";
import { useParams } from "react-router-dom";
import { api } from "../lib/api";
import { fullDate } from "../lib/format";
import { TYPE_META, type MemoryType } from "../lib/types";

export function Shared() {
  const { token = "" } = useParams();
  const q = useQuery({ queryKey: ["public-share", token], queryFn: () => api.publicShare(token), retry: false });
  const m = q.data?.memory;
  return (
    <div className="mx-auto max-w-2xl space-y-4 px-4 py-8">
      <div className="text-sm font-semibold text-accent">Shared from Memory</div>
      {q.isLoading && <div className="card h-48 animate-pulse" />}
      {q.isError && <div className="card p-6">This link is invalid or has expired.</div>}
      {m && (
        <article className="card space-y-4 p-5">
          {m.file_url && m.mime_type?.startsWith("image/") && <img src={m.file_url} alt="" className="w-full rounded-xl" />}
          {m.file_url && m.mime_type?.startsWith("audio/") && <audio controls src={m.file_url} className="w-full" />}
          <div className="text-sm text-zinc-500">
            {TYPE_META[m.type as MemoryType]?.emoji} {TYPE_META[m.type as MemoryType]?.label} · {m.created_at && fullDate(m.created_at)}
          </div>
          <h1 className="text-2xl font-bold">{m.title}</h1>
          {m.summary && <p>{m.summary}</p>}
          {m.source_url && (
            <a className="break-all text-accent underline" href={m.source_url} target="_blank" rel="noopener noreferrer">
              {m.source_url}
            </a>
          )}
          {m.content && <p className="whitespace-pre-wrap text-sm text-zinc-600 dark:text-zinc-400">{m.content}</p>}
          {m.file_url && (
            <a className="btn-ghost border border-zinc-300 dark:border-zinc-700" href={m.file_url}>
              Open original
            </a>
          )}
          <p className="text-xs text-zinc-500">Link expires {q.data && fullDate(q.data.expires_at)}.</p>
        </article>
      )}
    </div>
  );
}
