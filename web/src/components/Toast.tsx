import { useEffect, useState } from "react";

type Kind = "info" | "error";
interface Item {
  id: number;
  text: string;
  kind: Kind;
}

let listeners: ((items: Item[]) => void)[] = [];
let items: Item[] = [];
let seq = 0;

export function toast(text: string, kind: Kind = "info") {
  const item = { id: ++seq, text, kind };
  items = [...items, item].slice(-4);
  listeners.forEach((l) => l(items));
  setTimeout(() => {
    items = items.filter((i) => i.id !== item.id);
    listeners.forEach((l) => l(items));
  }, 4500);
}

export function Toaster() {
  const [list, setList] = useState<Item[]>(items);
  useEffect(() => {
    listeners.push(setList);
    return () => {
      listeners = listeners.filter((l) => l !== setList);
    };
  }, []);
  return (
    <div className="pointer-events-none fixed inset-x-0 bottom-20 z-50 flex flex-col items-center gap-2 px-4 sm:bottom-6" role="status" aria-live="polite">
      {list.map((t) => (
        <div
          key={t.id}
          className={`pointer-events-auto max-w-md rounded-xl px-4 py-2.5 text-sm shadow-lg ${
            t.kind === "error" ? "bg-red-600 text-white" : "bg-zinc-900 text-white dark:bg-white dark:text-zinc-900"
          }`}
        >
          {t.text}
        </div>
      ))}
    </div>
  );
}
