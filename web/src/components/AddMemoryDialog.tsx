import { useQueryClient } from "@tanstack/react-query";
import { Camera, FileText, Image, Link2, Mic, PenLine, Square, X } from "lucide-react";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { ApiError } from "../lib/api";
import { DOCUMENT_ACCEPT, DuplicateError, saveLink, saveNote, typeForFile, uploadFile } from "../lib/upload";
import type { Memory } from "../lib/types";
import { DuplicateDialog } from "./DuplicateDialog";
import { toast } from "./Toast";

type View = "menu" | "note" | "link" | "voice";

export function AddMemoryDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  const qc = useQueryClient();
  const [view, setView] = useState<View>("menu");
  const [busy, setBusy] = useState<string | null>(null);
  const [duplicate, setDuplicate] = useState<DuplicateError | null>(null);
  const cameraRef = useRef<HTMLInputElement>(null);
  const photoRef = useRef<HTMLInputElement>(null);
  const docRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (open) setView("menu");
  }, [open]);

  const done = (m: Memory) => {
    void qc.invalidateQueries({ queryKey: ["memories"] });
    toast(m.status === "ready" ? "✓ Memory saved" : "Saved — processing memory…");
    onClose();
  };

  const run = async (label: string, fn: () => Promise<Memory>) => {
    setBusy(label);
    try {
      done(await fn());
    } catch (e) {
      if (e instanceof DuplicateError) setDuplicate(e);
      else toast(e instanceof ApiError || e instanceof Error ? e.message : "Something went wrong.", "error");
    } finally {
      setBusy(null);
    }
  };

  const onFiles = (files: FileList | null) => {
    const list = Array.from(files ?? []);
    if (!list.length) return;
    // Upload sequentially; the UI returns immediately and processing is async.
    void (async () => {
      for (const f of list) {
        const type = typeForFile(f);
        await run(`Uploading ${f.name}…`, () =>
          uploadFile(f, type, { onProgress: (p) => setBusy(`Uploading ${f.name} — ${Math.round(p * 100)}%`) }),
        );
      }
    })();
  };

  if (!open) return null;

  return (
    <>
      <div className="fixed inset-0 z-40 flex items-end justify-center bg-black/40 p-0 sm:items-center sm:p-4" onClick={onClose}>
        <div
          role="dialog"
          aria-modal="true"
          aria-label="Add memory"
          className="card w-full max-w-md rounded-b-none p-5 sm:rounded-2xl"
          onClick={(e) => e.stopPropagation()}
        >
          <div className="mb-4 flex items-center justify-between">
            <h2 className="text-lg font-semibold">
              {view === "menu" ? "Add Memory" : view === "note" ? "Write Note" : view === "link" ? "Save Link" : "Record Voice"}
            </h2>
            <button className="btn-ghost p-2" onClick={onClose} aria-label="Close">
              <X className="size-5" />
            </button>
          </div>

          {busy && <p className="mb-3 rounded-lg bg-accent/10 px-3 py-2 text-sm text-accent">{busy}</p>}

          {view === "menu" && (
            <div className="grid grid-cols-2 gap-3">
              <Option icon={<Camera />} label="Take Photo" onClick={() => cameraRef.current?.click()} />
              <Option icon={<Image />} label="Choose Photo" onClick={() => photoRef.current?.click()} />
              <Option icon={<FileText />} label="Upload Document" onClick={() => docRef.current?.click()} />
              <Option icon={<Mic />} label="Record Voice" onClick={() => setView("voice")} />
              <Option icon={<PenLine />} label="Write Note" onClick={() => setView("note")} />
              <Option icon={<Link2 />} label="Save Link" onClick={() => setView("link")} />
              <input ref={cameraRef} type="file" accept="image/*" capture="environment" hidden onChange={(e) => onFiles(e.target.files)} />
              <input ref={photoRef} type="file" accept="image/*" multiple hidden onChange={(e) => onFiles(e.target.files)} />
              <input ref={docRef} type="file" accept={DOCUMENT_ACCEPT} multiple hidden onChange={(e) => onFiles(e.target.files)} />
            </div>
          )}

          {view === "note" && <NoteForm busy={!!busy} onSave={(text, title) => run("Saving note…", () => saveNote(text, title))} />}
          {view === "link" && <LinkForm busy={!!busy} onSave={(url, note) => run("Saving link…", () => saveLink(url, note))} />}
          {view === "voice" && (
            <VoiceRecorder
              busy={!!busy}
              onSave={(blob, ms) =>
                run("Uploading voice note…", () =>
                  uploadFile(new File([blob], `voice-${Date.now()}.webm`, { type: blob.type || "audio/webm" }), "voice", {
                    metadata: { duration_ms: ms },
                  }),
                )
              }
            />
          )}

          {view !== "menu" && (
            <button className="btn-ghost mt-3 w-full" onClick={() => setView("menu")}>
              Back
            </button>
          )}
        </div>
      </div>
      {duplicate && (
        <DuplicateDialog
          error={duplicate}
          onCancel={() => setDuplicate(null)}
          onChoose={(choice, replaceId) => {
            const d = duplicate;
            setDuplicate(null);
            void run("Saving…", () => d.retry(choice, replaceId));
          }}
        />
      )}
    </>
  );
}

function Option({ icon, label, onClick }: { icon: ReactNode; label: string; onClick: () => void }) {
  return (
    <button
      onClick={onClick}
      className="flex flex-col items-center gap-2 rounded-2xl border border-zinc-200 p-4 text-sm font-medium transition hover:border-accent hover:bg-accent/5 dark:border-zinc-800 [&_svg]:size-6 [&_svg]:text-accent"
    >
      {icon}
      {label}
    </button>
  );
}

function NoteForm({ busy, onSave }: { busy: boolean; onSave: (text: string, title: string) => void }) {
  const [title, setTitle] = useState("");
  const [text, setText] = useState("");
  return (
    <form
      className="space-y-3"
      onSubmit={(e) => {
        e.preventDefault();
        if (text.trim()) onSave(text.trim(), title.trim());
      }}
    >
      <input className="input" placeholder="Title (optional — Memory can name it)" value={title} onChange={(e) => setTitle(e.target.value)} />
      <textarea
        className="input min-h-40"
        placeholder="My laptop model is HP Omen 16 Max…"
        value={text}
        onChange={(e) => setText(e.target.value)}
        autoFocus
      />
      <button className="btn-primary w-full" disabled={busy || !text.trim()}>
        Save note
      </button>
    </form>
  );
}

function LinkForm({ busy, onSave }: { busy: boolean; onSave: (url: string, note: string) => void }) {
  const [url, setUrl] = useState("");
  const [note, setNote] = useState("");
  return (
    <form
      className="space-y-3"
      onSubmit={(e) => {
        e.preventDefault();
        const u = /^https?:\/\//i.test(url.trim()) ? url.trim() : `https://${url.trim()}`;
        onSave(u, note.trim());
      }}
    >
      <input className="input" type="text" inputMode="url" placeholder="https://…" value={url} onChange={(e) => setUrl(e.target.value)} autoFocus />
      <input className="input" placeholder="Why are you saving this? (optional)" value={note} onChange={(e) => setNote(e.target.value)} />
      <button className="btn-primary w-full" disabled={busy || !url.trim()}>
        Save link
      </button>
    </form>
  );
}

function VoiceRecorder({ busy, onSave }: { busy: boolean; onSave: (blob: Blob, durationMs: number) => void }) {
  const [state, setState] = useState<"idle" | "recording" | "recorded">("idle");
  const [elapsed, setElapsed] = useState(0);
  const [blob, setBlob] = useState<Blob | null>(null);
  const [error, setError] = useState("");
  const rec = useRef<MediaRecorder | null>(null);
  const start = useRef(0);

  useEffect(() => {
    if (state !== "recording") return;
    const t = setInterval(() => setElapsed(Date.now() - start.current), 250);
    return () => clearInterval(t);
  }, [state]);

  useEffect(() => () => rec.current?.stream.getTracks().forEach((t) => t.stop()), []);

  const begin = async () => {
    setError("");
    try {
      const stream = await navigator.mediaDevices.getUserMedia({ audio: true });
      const mime = MediaRecorder.isTypeSupported("audio/webm") ? "audio/webm" : "audio/mp4";
      const r = new MediaRecorder(stream, { mimeType: mime });
      const parts: Blob[] = [];
      r.ondataavailable = (e) => e.data.size && parts.push(e.data);
      r.onstop = () => {
        stream.getTracks().forEach((t) => t.stop());
        setBlob(new Blob(parts, { type: mime }));
        setState("recorded");
      };
      r.start();
      rec.current = r;
      start.current = Date.now();
      setElapsed(0);
      setState("recording");
    } catch {
      setError("Microphone access was denied.");
    }
  };

  const secs = Math.floor(elapsed / 1000);
  return (
    <div className="flex flex-col items-center gap-4 py-2">
      <div className="font-mono text-3xl tabular-nums">
        {String(Math.floor(secs / 60)).padStart(2, "0")}:{String(secs % 60).padStart(2, "0")}
      </div>
      {state === "idle" && (
        <button className="btn-primary size-20 rounded-full" onClick={begin} aria-label="Start recording">
          <Mic className="size-8" />
        </button>
      )}
      {state === "recording" && (
        <button className="btn-danger size-20 rounded-full" onClick={() => rec.current?.stop()} aria-label="Stop recording">
          <Square className="size-7" />
        </button>
      )}
      {state === "recorded" && blob && (
        <>
          <audio controls src={URL.createObjectURL(blob)} className="w-full" />
          <div className="flex w-full gap-2">
            <button className="btn-ghost flex-1" onClick={() => setState("idle")}>
              Re-record
            </button>
            <button className="btn-primary flex-1" disabled={busy} onClick={() => onSave(blob, elapsed)}>
              Save voice note
            </button>
          </div>
        </>
      )}
      {error && <p className="text-sm text-red-600">{error}</p>}
    </div>
  );
}
