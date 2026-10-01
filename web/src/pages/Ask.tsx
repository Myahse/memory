import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { History, Send, SquarePen } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { useLocation, useNavigate, useParams } from "react-router-dom";
import { MemoryCard } from "../components/MemoryCard";
import { api } from "../lib/api";
import type { ChatMessage, Memory } from "../lib/types";

interface Turn {
  id: string;
  role: "user" | "assistant";
  content: string;
  sources: Memory[];
  pending?: boolean;
}

const EXAMPLES = [
  "What laptop was I looking at last month?",
  "Find the receipt from Carrefour.",
  "What did I save about my university application?",
  "When did I write down this idea?",
];

export function AskPage() {
  const { id } = useParams();
  const nav = useNavigate();
  const location = useLocation();
  const qc = useQueryClient();
  const [input, setInput] = useState("");
  const [turns, setTurns] = useState<Turn[]>([]);
  const [showHistory, setShowHistory] = useState(false);
  const bottom = useRef<HTMLDivElement>(null);
  const conversationId = useRef<string | undefined>(id);
  const scoped = useRef<string[] | undefined>((location.state as { memoryIds?: string[] } | null)?.memoryIds);

  const convo = useQuery({ queryKey: ["conversation", id], queryFn: () => api.conversation(id!), enabled: !!id });
  const history = useQuery({ queryKey: ["conversations"], queryFn: api.conversations, enabled: showHistory });

  useEffect(() => {
    conversationId.current = id;
    if (!id) setTurns([]);
  }, [id]);
  useEffect(() => {
    if (convo.data) setTurns(convo.data.messages.map((m: ChatMessage) => ({ ...m })));
  }, [convo.data]);
  useEffect(() => bottom.current?.scrollIntoView({ behavior: "smooth" }), [turns]);

  const ask = useMutation({
    mutationFn: (q: string) => api.ask(q, conversationId.current, scoped.current),
    onMutate: (q) => {
      setTurns((t) => [
        ...t,
        { id: crypto.randomUUID(), role: "user", content: q, sources: [] },
        { id: "pending", role: "assistant", content: "", sources: [], pending: true },
      ]);
    },
    onSuccess: (res) => {
      scoped.current = undefined; // only the first question is scoped to search results
      setTurns((t) => [...t.filter((x) => !x.pending), { id: res.message_id, role: "assistant", content: res.answer, sources: res.sources }]);
      if (!conversationId.current) {
        conversationId.current = res.conversation_id;
        nav(`/ask/${res.conversation_id}`, { replace: true });
      }
      void qc.invalidateQueries({ queryKey: ["conversations"] });
    },
    onError: (e: Error) => {
      setTurns((t) => [...t.filter((x) => !x.pending), { id: crypto.randomUUID(), role: "assistant", content: `⚠️ ${e.message}`, sources: [] }]);
    },
  });

  // Question handed over from search results.
  useEffect(() => {
    const st = location.state as { question?: string } | null;
    if (st?.question && turns.length === 0 && !ask.isPending) {
      ask.mutate(st.question);
      nav(location.pathname, { replace: true, state: null });
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const submit = (q: string) => {
    if (!q.trim() || ask.isPending) return;
    setInput("");
    ask.mutate(q.trim());
  };

  return (
    <div className="flex min-h-[calc(100vh-10rem)] flex-col">
      <div className="mb-4 flex items-center justify-between">
        <h1 className="text-xl font-semibold">Ask Memory</h1>
        <div className="flex gap-1">
          <button className="btn-ghost p-2" onClick={() => setShowHistory((s) => !s)} aria-label="Conversation history">
            <History className="size-5" />
          </button>
          <button className="btn-ghost p-2" onClick={() => nav("/ask")} aria-label="New conversation">
            <SquarePen className="size-5" />
          </button>
        </div>
      </div>

      {showHistory && (
        <div className="card mb-4 max-h-64 overflow-y-auto p-2">
          {history.data?.conversations.length === 0 && <p className="p-2 text-sm text-zinc-500">No conversations yet.</p>}
          {history.data?.conversations.map((c) => (
            <button
              key={c.id}
              className="block w-full truncate rounded-lg px-3 py-2 text-left text-sm hover:bg-zinc-100 dark:hover:bg-zinc-800"
              onClick={() => {
                setShowHistory(false);
                nav(`/ask/${c.id}`);
              }}
            >
              {c.title || "Conversation"}
            </button>
          ))}
        </div>
      )}

      <div className="flex-1 space-y-4">
        {turns.length === 0 && !convo.isLoading && (
          <div className="space-y-2">
            <p className="text-zinc-500">Ask anything about what you've saved. Answers only use your own memories, with sources.</p>
            {EXAMPLES.map((e) => (
              <button key={e} className="card block w-full p-3 text-left text-sm hover:border-accent" onClick={() => submit(e)}>
                {e}
              </button>
            ))}
          </div>
        )}
        {turns.map((t) =>
          t.role === "user" ? (
            <div key={t.id} className="flex justify-end">
              <div className="max-w-[85%] rounded-2xl rounded-br-md bg-accent px-4 py-2.5 text-white">{t.content}</div>
            </div>
          ) : (
            <div key={t.id} className="space-y-3">
              <div className="max-w-[90%] rounded-2xl rounded-bl-md bg-white px-4 py-3 shadow-sm dark:bg-zinc-900">
                {t.pending ? <span className="animate-pulse text-zinc-500">Searching your memories…</span> : t.content}
              </div>
              {t.sources.length > 0 && (
                <div className="space-y-2">
                  <p className="text-xs font-semibold uppercase tracking-wide text-zinc-500">
                    Based on {t.sources.length} {t.sources.length === 1 ? "memory" : "memories"}
                  </p>
                  {t.sources.map((m) => (
                    <MemoryCard key={m.id} memory={m} compact />
                  ))}
                </div>
              )}
            </div>
          ),
        )}
        <div ref={bottom} />
      </div>

      <form
        className="sticky bottom-20 mt-4 flex gap-2 sm:bottom-4"
        onSubmit={(e) => {
          e.preventDefault();
          submit(input);
        }}
      >
        <input className="input rounded-2xl py-3 shadow-sm" placeholder="Ask your Memory…" value={input} onChange={(e) => setInput(e.target.value)} />
        <button className="btn-primary rounded-2xl px-4" disabled={ask.isPending || !input.trim()} aria-label="Send">
          <Send className="size-5" />
        </button>
      </form>
    </div>
  );
}
