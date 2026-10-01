import { CalendarDays, Home, MessageCircle, Plus, Settings } from "lucide-react";
import { useState, type ReactNode } from "react";
import { NavLink, Outlet } from "react-router-dom";
import { useAuth } from "../auth/AuthProvider";
import { useMemoryRealtime } from "../lib/realtime";
import { AddMemoryDialog } from "./AddMemoryDialog";

export function Layout() {
  const { session } = useAuth();
  const [adding, setAdding] = useState(false);
  useMemoryRealtime(session?.user.id);

  return (
    <div className="mx-auto flex min-h-screen max-w-3xl flex-col">
      <header className="sticky top-0 z-30 flex items-center justify-between border-b border-zinc-200/70 bg-zinc-50/80 px-4 py-3 backdrop-blur dark:border-zinc-800/70 dark:bg-zinc-950/80">
        <NavLink to="/" className="text-xl font-bold tracking-tight">
          Memory
        </NavLink>
        <nav className="hidden items-center gap-1 sm:flex">
          <Tab to="/" icon={<Home />} label="Home" />
          <Tab to="/ask" icon={<MessageCircle />} label="Ask" />
          <Tab to="/timeline" icon={<CalendarDays />} label="Timeline" />
          <Tab to="/settings" icon={<Settings />} label="Settings" />
          <button className="btn-primary ml-2" onClick={() => setAdding(true)}>
            <Plus className="size-4" /> Add Memory
          </button>
        </nav>
      </header>

      <main className="flex-1 px-4 pb-28 pt-4 sm:pb-10">
        <Outlet context={{ openAdd: () => setAdding(true) }} />
      </main>

      {/* Mobile bottom bar */}
      <nav className="fixed inset-x-0 bottom-0 z-30 flex items-center justify-around border-t border-zinc-200 bg-white/95 px-2 py-2 backdrop-blur sm:hidden dark:border-zinc-800 dark:bg-zinc-950/95">
        <Tab to="/" icon={<Home />} label="Home" />
        <Tab to="/ask" icon={<MessageCircle />} label="Ask" />
        <button className="btn-primary size-12 rounded-full p-0" onClick={() => setAdding(true)} aria-label="Add Memory">
          <Plus className="size-6" />
        </button>
        <Tab to="/timeline" icon={<CalendarDays />} label="Timeline" />
        <Tab to="/settings" icon={<Settings />} label="Settings" />
      </nav>

      <AddMemoryDialog open={adding} onClose={() => setAdding(false)} />
    </div>
  );
}

function Tab({ to, icon, label }: { to: string; icon: ReactNode; label: string }) {
  return (
    <NavLink
      to={to}
      end={to === "/"}
      className={({ isActive }) =>
        `flex flex-col items-center gap-0.5 rounded-xl px-3 py-1.5 text-[11px] font-medium sm:flex-row sm:gap-2 sm:text-sm [&_svg]:size-5 sm:[&_svg]:size-4 ${
          isActive ? "text-accent" : "text-zinc-500 hover:text-zinc-900 dark:hover:text-zinc-100"
        }`
      }
    >
      {icon}
      {label}
    </NavLink>
  );
}

export interface LayoutContext {
  openAdd: () => void;
}
