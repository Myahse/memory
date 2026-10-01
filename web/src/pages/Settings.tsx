import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { LogOut } from "lucide-react";
import { useEffect, useState } from "react";
import { toast } from "../components/Toast";
import { api } from "../lib/api";
import { bytes, fullDate } from "../lib/format";
import { supabase } from "../lib/supabase";
import { applyTheme, getTheme, type Theme } from "../lib/theme";
import { Modal } from "./MemoryDetail";

export function Settings() {
  const qc = useQueryClient();
  const me = useQuery({ queryKey: ["me"], queryFn: api.me });
  const [name, setName] = useState("");
  const [theme, setTheme] = useState<Theme>(getTheme());
  const [deleting, setDeleting] = useState(false);

  useEffect(() => {
    if (me.data) setName(me.data.profile.name);
  }, [me.data]);

  const save = useMutation({
    mutationFn: () => api.updateMe({ name }),
    onSuccess: (d) => {
      qc.setQueryData(["me"], d);
      toast("Profile saved");
    },
    onError: (e: Error) => toast(e.message, "error"),
  });

  const usage = me.data?.usage;
  const profile = me.data?.profile;

  return (
    <div className="space-y-6">
      <h1 className="text-xl font-semibold">Profile & Settings</h1>

      <section className="card space-y-4 p-4">
        <div className="flex items-center gap-4">
          {profile?.avatar_url ? (
            <img src={profile.avatar_url} alt="" className="size-14 rounded-full object-cover" />
          ) : (
            <div className="flex size-14 items-center justify-center rounded-full bg-accent/15 text-xl font-semibold text-accent">
              {(profile?.name || profile?.email || "?").slice(0, 1).toUpperCase()}
            </div>
          )}
          <div>
            <div className="font-medium">{profile?.name || "Unnamed"}</div>
            <div className="text-sm text-zinc-500">{profile?.email}</div>
            {profile && <div className="text-xs text-zinc-500">Member since {fullDate(profile.created_at)}</div>}
          </div>
        </div>
        <form
          className="flex gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            save.mutate();
          }}
        >
          <input className="input" value={name} onChange={(e) => setName(e.target.value)} placeholder="Your name" aria-label="Name" />
          <button className="btn-primary" disabled={save.isPending}>
            Save
          </button>
        </form>
      </section>

      {usage && (
        <section className="card space-y-3 p-4">
          <div className="flex items-center justify-between">
            <h2 className="font-medium">Plan</h2>
            <span className="chip border-accent text-accent">{usage.plan === "pro" ? "Pro" : "Free"}</span>
          </div>
          <Meter label="Storage" used={usage.storage_used} limit={usage.storage_limit} fmt={bytes} />
          <Meter label="AI questions this month" used={usage.ai_queries} limit={usage.ai_query_limit} />
          <Meter label="Items processed this month" used={usage.processed_items} limit={usage.processed_limit} />
          <p className="text-sm text-zinc-500">{usage.memory_count} memories saved.</p>
        </section>
      )}

      <section className="card space-y-3 p-4">
        <h2 className="font-medium">Appearance</h2>
        <div className="grid grid-cols-3 gap-2">
          {(["light", "dark", "system"] as Theme[]).map((t) => (
            <button
              key={t}
              className={`btn border capitalize ${theme === t ? "border-accent text-accent" : "border-zinc-300 dark:border-zinc-700"}`}
              onClick={() => {
                setTheme(t);
                applyTheme(t);
              }}
            >
              {t}
            </button>
          ))}
        </div>
      </section>

      <section className="card space-y-3 p-4">
        <h2 className="font-medium">Privacy</h2>
        <p className="text-sm text-zinc-500">
          Your memories are private to your account: they're protected by row-level security, stored in a private bucket and only
          ever served through short-lived signed links.
        </p>
        <button className="btn-ghost w-full border border-zinc-300 dark:border-zinc-700" onClick={() => supabase.auth.signOut()}>
          <LogOut className="size-4" /> Sign out
        </button>
        <button className="btn-danger w-full" onClick={() => setDeleting(true)}>
          Delete my data
        </button>
      </section>

      {deleting && <DeleteAllDialog onClose={() => setDeleting(false)} />}
    </div>
  );
}

function Meter({ label, used, limit, fmt = String }: { label: string; used: number; limit: number; fmt?: (n: number) => string }) {
  const pct = limit > 0 ? Math.min(100, (used / limit) * 100) : 0;
  return (
    <div className="space-y-1 text-sm">
      <div className="flex justify-between">
        <span>{label}</span>
        <span className="text-zinc-500">
          {fmt(used)} / {limit > 0 ? fmt(limit) : "Unlimited"}
        </span>
      </div>
      {limit > 0 && (
        <div className="h-2 overflow-hidden rounded-full bg-zinc-200 dark:bg-zinc-800">
          <div className={`h-full rounded-full ${pct > 90 ? "bg-red-500" : "bg-accent"}`} style={{ width: `${pct}%` }} />
        </div>
      )}
    </div>
  );
}

function DeleteAllDialog({ onClose }: { onClose: () => void }) {
  const [phrase, setPhrase] = useState("");
  const [account, setAccount] = useState(false);
  const qc = useQueryClient();
  const del = useMutation({
    mutationFn: () => api.deleteMyData(account),
    onSuccess: async () => {
      qc.clear();
      toast("All your data was permanently deleted.");
      if (account) await supabase.auth.signOut();
      onClose();
    },
    onError: (e: Error) => toast(e.message, "error"),
  });
  return (
    <Modal title="Delete my data" onClose={onClose}>
      <div className="space-y-4 text-sm">
        <p>This permanently deletes, with no way to recover:</p>
        <ul className="list-inside list-disc text-zinc-600 dark:text-zinc-400">
          <li>all memories and their metadata</li>
          <li>all uploaded files (photos, documents, audio)</li>
          <li>all embeddings and search indexes</li>
          <li>all tags and Ask Memory conversations</li>
          <li>all share links</li>
        </ul>
        <label className="flex items-center gap-2">
          <input type="checkbox" checked={account} onChange={(e) => setAccount(e.target.checked)} />
          Also delete my account
        </label>
        <label className="block space-y-1">
          <span>
            Type <strong>DELETE MY DATA</strong> to confirm
          </span>
          <input className="input" value={phrase} onChange={(e) => setPhrase(e.target.value)} autoComplete="off" />
        </label>
        <button className="btn-danger w-full" disabled={phrase !== "DELETE MY DATA" || del.isPending} onClick={() => del.mutate()}>
          {del.isPending ? "Deleting…" : "Permanently delete everything"}
        </button>
      </div>
    </Modal>
  );
}
