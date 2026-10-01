import { useState, type FormEvent } from "react";
import { Link, Navigate, useNavigate } from "react-router-dom";
import { signInWithProvider, useAuth } from "../auth/AuthProvider";
import { configError, supabase } from "../lib/supabase";

export function Welcome() {
  const { session } = useAuth();
  if (session) return <Navigate to="/" replace />;
  return (
    <div className="mx-auto flex min-h-screen max-w-md flex-col justify-center gap-8 px-6">
      <div className="space-y-3">
        <div className="flex size-14 items-center justify-center rounded-2xl bg-accent text-2xl text-white">M</div>
        <h1 className="text-4xl font-bold tracking-tight">Memory</h1>
        <p className="text-xl text-zinc-600 dark:text-zinc-400">Save anything. Find anything.</p>
        <p className="text-zinc-500">
          Screenshots, receipts, documents, voice notes and links — understood automatically, private by default, and
          searchable by simply asking.
        </p>
      </div>
      <div className="space-y-3">
        <Link to="/register" className="btn-primary w-full">
          Create account
        </Link>
        <Link to="/login" className="btn-ghost w-full border border-zinc-300 dark:border-zinc-700">
          Sign in
        </Link>
      </div>
      {configError && <p className="text-sm text-red-600">{configError}</p>}
    </div>
  );
}

function AuthShell({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="mx-auto flex min-h-screen max-w-sm flex-col justify-center gap-6 px-6">
      <Link to="/welcome" className="text-2xl font-bold">
        Memory
      </Link>
      <h1 className="text-xl font-semibold">{title}</h1>
      {children}
    </div>
  );
}

function OAuthButtons({ onError }: { onError: (m: string) => void }) {
  const go = (p: "google" | "apple") => signInWithProvider(p).catch((e: Error) => onError(e.message));
  return (
    <div className="space-y-2">
      <button type="button" className="btn-ghost w-full border border-zinc-300 dark:border-zinc-700" onClick={() => go("google")}>
        Continue with Google
      </button>
      <button type="button" className="btn-ghost w-full border border-zinc-300 dark:border-zinc-700" onClick={() => go("apple")}>
        Continue with Apple
      </button>
      <div className="flex items-center gap-3 py-2 text-xs text-zinc-400">
        <span className="h-px flex-1 bg-zinc-200 dark:bg-zinc-800" /> or <span className="h-px flex-1 bg-zinc-200 dark:bg-zinc-800" />
      </div>
    </div>
  );
}

export function Login() {
  const { session } = useAuth();
  const nav = useNavigate();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  if (session) return <Navigate to="/" replace />;

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError("");
    const { error } = await supabase.auth.signInWithPassword({ email, password });
    setBusy(false);
    if (error) setError(error.message === "Invalid login credentials" ? "Wrong email or password." : error.message);
    else nav("/");
  };

  return (
    <AuthShell title="Welcome back">
      <OAuthButtons onError={setError} />
      <form className="space-y-3" onSubmit={submit}>
        <input className="input" type="email" autoComplete="email" placeholder="Email" value={email} onChange={(e) => setEmail(e.target.value)} required />
        <input className="input" type="password" autoComplete="current-password" placeholder="Password" value={password} onChange={(e) => setPassword(e.target.value)} required />
        {error && <p className="text-sm text-red-600">{error}</p>}
        <button className="btn-primary w-full" disabled={busy}>
          {busy ? "Signing in…" : "Sign in"}
        </button>
      </form>
      <p className="text-sm text-zinc-500">
        New to Memory?{" "}
        <Link className="text-accent" to="/register">
          Create an account
        </Link>
      </p>
    </AuthShell>
  );
}

export function Register() {
  const { session } = useAuth();
  const [name, setName] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [sent, setSent] = useState(false);
  const [busy, setBusy] = useState(false);
  if (session) return <Navigate to="/" replace />;

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (password.length < 8) return setError("Use at least 8 characters for your password.");
    setBusy(true);
    setError("");
    const { data, error } = await supabase.auth.signUp({
      email,
      password,
      options: { data: { name }, emailRedirectTo: `${window.location.origin}/` },
    });
    setBusy(false);
    if (error) setError(error.message);
    else if (!data.session) setSent(true);
  };

  if (sent)
    return (
      <AuthShell title="Check your email">
        <p className="text-zinc-600 dark:text-zinc-400">We sent a confirmation link to {email}. Open it to finish creating your account.</p>
      </AuthShell>
    );

  return (
    <AuthShell title="Create your Memory">
      <OAuthButtons onError={setError} />
      <form className="space-y-3" onSubmit={submit}>
        <input className="input" placeholder="Name" autoComplete="name" value={name} onChange={(e) => setName(e.target.value)} />
        <input className="input" type="email" autoComplete="email" placeholder="Email" value={email} onChange={(e) => setEmail(e.target.value)} required />
        <input className="input" type="password" autoComplete="new-password" placeholder="Password (8+ characters)" value={password} onChange={(e) => setPassword(e.target.value)} required />
        {error && <p className="text-sm text-red-600">{error}</p>}
        <button className="btn-primary w-full" disabled={busy}>
          {busy ? "Creating…" : "Create account"}
        </button>
      </form>
      <p className="text-xs text-zinc-500">Your memories are private. Only you can see them.</p>
    </AuthShell>
  );
}
