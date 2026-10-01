import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowLeft, Download, Pencil, RotateCw, Share2, Trash2 } from "lucide-react";
import { useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { displayTitle, MemoryCard, StatusBadge } from "../components/MemoryCard";
import { toast } from "../components/Toast";
import { api } from "../lib/api";
import { bytes, fullDate } from "../lib/format";
import { TYPE_META, type Memory } from "../lib/types";

export function MemoryDetail() {
  const { id = "" } = useParams();
  const nav = useNavigate();
  const qc = useQueryClient();
  const [editing, setEditing] = useState(false);
  const [sharing, setSharing] = useState(false);

  const q = useQuery({
    queryKey: ["memory", id],
    queryFn: () => api.getMemory(id).then((r) => r.memory),
    refetchInterval: (query) => (["pending", "processing"].includes(query.state.data?.status ?? "") ? 3000 : false),
  });
  const related = useQuery({
    queryKey: ["related", id],
    queryFn: () => api.related(id),
    enabled: q.data?.status === "ready",
  });

  const del = useMutation({
    mutationFn: () => api.deleteMemory(id),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["memories"] });
      toast("Memory deleted");
      nav("/", { replace: true });
    },
    onError: (e: Error) => toast(e.message, "error"),
  });
  const retry = useMutation({
    mutationFn: () => api.retryMemory(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["memory", id] }),
    onError: (e: Error) => toast(e.message, "error"),
  });

  if (q.isLoading) return <div className="card h-64 animate-pulse" />;
  if (q.isError || !q.data)
    return (
      <div className="card p-6 text-center">
        <p>This memory doesn't exist or was deleted.</p>
        <Link to="/" className="btn-ghost mt-3">
          Go home
        </Link>
      </div>
    );
  const m = q.data;
  const meta = TYPE_META[m.type];
  const entities = (m.metadata?.entities ?? {}) as Record<string, string[]>;
  const receipt = m.metadata?.receipt as { merchant?: string; total?: number; currency?: string; date?: string } | undefined;
  const dup = m.metadata?.possible_duplicate_of as string | undefined;

  const download = async () => {
    try {
      const { url } = await api.download(m.id);
      window.location.href = url;
    } catch (e) {
      toast((e as Error).message, "error");
    }
  };

  return (
    <article className="space-y-5">
      <button className="btn-ghost -ml-3" onClick={() => nav(-1)}>
        <ArrowLeft className="size-4" /> Back
      </button>

      <Preview memory={m} />

      <header className="space-y-1">
        <div className="text-sm text-zinc-500">
          {meta.emoji} {meta.label} · {fullDate(m.captured_at ?? m.created_at)}
          {m.category && <> · {m.category}</>}
        </div>
        <h1 className="text-2xl font-bold">{displayTitle(m)}</h1>
        <StatusBadge memory={m} />
      </header>

      {m.status === "failed" && (
        <div className="card flex items-center justify-between gap-3 border-red-300 p-4 dark:border-red-900">
          <p className="text-sm text-red-700 dark:text-red-400">{m.processing_error || "Processing failed."}</p>
          <button className="btn-primary shrink-0" onClick={() => retry.mutate()} disabled={retry.isPending}>
            <RotateCw className="size-4" /> Retry
          </button>
        </div>
      )}
      {dup && (
        <Link to={`/memory/${dup}`} className="card block p-3 text-sm text-amber-700 dark:text-amber-400">
          This looks similar to an existing memory → view it
        </Link>
      )}

      <div className="flex flex-wrap gap-2">
        <button className="btn-ghost border border-zinc-300 dark:border-zinc-700" onClick={() => setEditing(true)}>
          <Pencil className="size-4" /> Edit
        </button>
        <button className="btn-ghost border border-zinc-300 dark:border-zinc-700" onClick={() => setSharing(true)}>
          <Share2 className="size-4" /> Share
        </button>
        {m.has_file && (
          <button className="btn-ghost border border-zinc-300 dark:border-zinc-700" onClick={download}>
            <Download className="size-4" /> Download
          </button>
        )}
        <button
          className="btn-ghost border border-red-300 text-red-600 dark:border-red-900"
          onClick={() => confirm("Delete this memory and its file permanently?") && del.mutate()}
        >
          <Trash2 className="size-4" /> Delete
        </button>
      </div>

      {m.summary && <Section title="Summary">{m.summary}</Section>}
      {m.tags.length > 0 && (
        <Section title="Tags">
          <div className="flex flex-wrap gap-2">
            {m.tags.map((t) => (
              <Link key={t} to={`/search?q=${encodeURIComponent(t)}`} className="chip border-zinc-300 dark:border-zinc-700">
                #{t}
              </Link>
            ))}
          </div>
        </Section>
      )}
      {receipt && (
        <Section title="Receipt">
          {receipt.merchant} {receipt.total ? `— ${receipt.total} ${receipt.currency ?? ""}` : ""} {receipt.date ? `(${receipt.date})` : ""}
        </Section>
      )}
      {Object.entries(entities).some(([, v]) => v?.length) && (
        <Section title="Details">
          <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-sm">
            {Object.entries(entities)
              .filter(([, v]) => v?.length)
              .map(([k, v]) => (
                <div key={k} className="contents">
                  <dt className="capitalize text-zinc-500">{k}</dt>
                  <dd>{v.join(", ")}</dd>
                </div>
              ))}
          </dl>
        </Section>
      )}
      {m.source_url && (
        <Section title="Link">
          <a href={m.source_url} target="_blank" rel="noopener noreferrer" className="break-all text-accent underline">
            {m.source_url}
          </a>
        </Section>
      )}
      {m.content && (
        <Section title={m.type === "voice" ? "Transcript" : m.type === "note" ? "Note" : "Extracted text"}>
          <p className="max-h-96 overflow-y-auto whitespace-pre-wrap text-sm leading-relaxed">{m.content}</p>
        </Section>
      )}
      {m.has_file && (
        <p className="text-xs text-zinc-500">
          Original file · {m.mime_type} · {bytes(m.file_size)}
        </p>
      )}

      {related.data && related.data.related.length > 0 && (
        <section className="space-y-2">
          <h2 className="text-sm font-semibold uppercase tracking-wide text-zinc-500">Related memories</h2>
          {related.data.related.map((r) => (
            <MemoryCard key={r.memory.id} memory={r.memory} compact />
          ))}
        </section>
      )}

      {editing && <EditDialog memory={m} onClose={() => setEditing(false)} />}
      {sharing && <ShareDialog memory={m} onClose={() => setSharing(false)} />}
    </article>
  );
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="card space-y-2 p-4">
      <h2 className="text-xs font-semibold uppercase tracking-wide text-zinc-500">{title}</h2>
      <div>{children}</div>
    </section>
  );
}

function Preview({ memory: m }: { memory: Memory }) {
  if (["photo", "screenshot", "receipt"].includes(m.type) && (m.file_url || m.thumbnail_url))
    return <img src={m.file_url || m.thumbnail_url} alt={m.title} className="max-h-[60vh] w-full rounded-2xl bg-zinc-100 object-contain dark:bg-zinc-900" />;
  if (m.type === "voice" && m.file_url) return <audio controls src={m.file_url} className="w-full" />;
  if (m.type === "pdf" && m.file_url)
    return <iframe src={m.file_url} title={m.title} className="h-[60vh] w-full rounded-2xl border border-zinc-200 dark:border-zinc-800" />;
  if (m.thumbnail_url) return <img src={m.thumbnail_url} alt="" className="max-h-64 w-full rounded-2xl object-cover" />;
  return null;
}

function EditDialog({ memory, onClose }: { memory: Memory; onClose: () => void }) {
  const qc = useQueryClient();
  const [title, setTitle] = useState(memory.title);
  const [summary, setSummary] = useState(memory.summary);
  const [category, setCategory] = useState(memory.category);
  const [tags, setTags] = useState(memory.tags.join(", "));
  const [content, setContent] = useState(memory.content ?? "");
  const save = useMutation({
    mutationFn: () =>
      api.updateMemory(memory.id, {
        title,
        summary,
        category,
        content: memory.type === "note" ? content : undefined,
        tags: tags
          .split(",")
          .map((t) => t.trim())
          .filter(Boolean),
      }),
    onSuccess: (r) => {
      qc.setQueryData(["memory", memory.id], r.memory);
      void qc.invalidateQueries({ queryKey: ["memories"] });
      toast("Saved");
      onClose();
    },
    onError: (e: Error) => toast(e.message, "error"),
  });
  return (
    <Modal title="Edit memory" onClose={onClose}>
      <form
        className="space-y-3"
        onSubmit={(e) => {
          e.preventDefault();
          save.mutate();
        }}
      >
        <label className="block space-y-1 text-sm">
          <span className="text-zinc-500">Title</span>
          <input className="input" value={title} onChange={(e) => setTitle(e.target.value)} />
        </label>
        <label className="block space-y-1 text-sm">
          <span className="text-zinc-500">Summary</span>
          <textarea className="input min-h-20" value={summary} onChange={(e) => setSummary(e.target.value)} />
        </label>
        <label className="block space-y-1 text-sm">
          <span className="text-zinc-500">Category</span>
          <input className="input" value={category} onChange={(e) => setCategory(e.target.value)} />
        </label>
        <label className="block space-y-1 text-sm">
          <span className="text-zinc-500">Tags (comma separated)</span>
          <input className="input" value={tags} onChange={(e) => setTags(e.target.value)} />
        </label>
        {memory.type === "note" && (
          <label className="block space-y-1 text-sm">
            <span className="text-zinc-500">Note</span>
            <textarea className="input min-h-32" value={content} onChange={(e) => setContent(e.target.value)} />
          </label>
        )}
        <button className="btn-primary w-full" disabled={save.isPending}>
          Save
        </button>
      </form>
    </Modal>
  );
}

function ShareDialog({ memory, onClose }: { memory: Memory; onClose: () => void }) {
  const qc = useQueryClient();
  const [hours, setHours] = useState(24);
  const [created, setCreated] = useState<string | null>(null);
  const shares = useQuery({ queryKey: ["shares", memory.id], queryFn: () => api.listShares(memory.id) });
  const create = useMutation({
    mutationFn: () => api.createShare(memory.id, hours),
    onSuccess: (s) => {
      setCreated(s.url ?? null);
      void qc.invalidateQueries({ queryKey: ["shares", memory.id] });
    },
    onError: (e: Error) => toast(e.message, "error"),
  });
  const revoke = useMutation({
    mutationFn: (sid: string) => api.revokeShare(sid),
    onSuccess: () => {
      setCreated(null);
      toast("Link revoked");
      void qc.invalidateQueries({ queryKey: ["shares", memory.id] });
    },
  });
  return (
    <Modal title="Share this memory" onClose={onClose}>
      <div className="space-y-4">
        <p className="text-sm text-zinc-500">
          Creates a temporary secure link to <strong>this memory only</strong>. The rest of your archive stays private. You can
          revoke it any time.
        </p>
        <div className="flex gap-2">
          <select className="input" value={hours} onChange={(e) => setHours(Number(e.target.value))}>
            <option value={1}>1 hour</option>
            <option value={24}>1 day</option>
            <option value={168}>7 days</option>
            <option value={720}>30 days</option>
          </select>
          <button className="btn-primary shrink-0" onClick={() => create.mutate()} disabled={create.isPending}>
            Create link
          </button>
        </div>
        {created && (
          <div className="space-y-2">
            <input className="input font-mono text-xs" readOnly value={created} onFocus={(e) => e.target.select()} />
            <button
              className="btn-ghost w-full"
              onClick={() => navigator.clipboard.writeText(created).then(() => toast("Link copied"))}
            >
              Copy link
            </button>
          </div>
        )}
        {(shares.data?.shares.length ?? 0) > 0 && (
          <div className="space-y-2">
            <h3 className="text-xs font-semibold uppercase text-zinc-500">Active links</h3>
            {shares.data!.shares.map((s) => (
              <div key={s.id} className="flex items-center justify-between text-sm">
                <span>Expires {fullDate(s.expires_at)}</span>
                <button className="text-red-600 hover:underline" onClick={() => revoke.mutate(s.id)}>
                  Revoke
                </button>
              </div>
            ))}
          </div>
        )}
      </div>
    </Modal>
  );
}

export function Modal({ title, onClose, children }: { title: string; onClose: () => void; children: React.ReactNode }) {
  return (
    <div className="fixed inset-0 z-40 flex items-end justify-center bg-black/40 sm:items-center sm:p-4" onClick={onClose}>
      <div role="dialog" aria-modal="true" aria-label={title} className="card max-h-[90vh] w-full max-w-md overflow-y-auto rounded-b-none p-5 sm:rounded-2xl" onClick={(e) => e.stopPropagation()}>
        <h2 className="mb-4 text-lg font-semibold">{title}</h2>
        {children}
      </div>
    </div>
  );
}
