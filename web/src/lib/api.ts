import { supabase } from "./supabase";
import type {
  AskResponse,
  ChatMessage,
  Conversation,
  Facet,
  Memory,
  MemoryType,
  Profile,
  SearchFilters,
  SearchHit,
  Share,
  Usage,
} from "./types";

const API_URL = ((import.meta.env.VITE_API_URL as string | undefined) ?? "http://localhost:8080").replace(/\/$/, "");

export class ApiError extends Error {
  status: number;
  code: string;
  body: Record<string, unknown>;
  constructor(status: number, code: string, message: string, body: Record<string, unknown>) {
    super(message);
    this.status = status;
    this.code = code;
    this.body = body;
  }
}

async function request<T>(method: string, path: string, body?: unknown, auth = true): Promise<T> {
  const headers: Record<string, string> = {};
  if (body !== undefined) headers["Content-Type"] = "application/json";
  if (auth) {
    const { data } = await supabase.auth.getSession();
    const token = data.session?.access_token;
    if (!token) throw new ApiError(401, "unauthenticated", "Please sign in again.", {});
    headers.Authorization = `Bearer ${token}`;
  }
  let res: Response;
  try {
    res = await fetch(API_URL + path, { method, headers, body: body === undefined ? undefined : JSON.stringify(body) });
  } catch {
    throw new ApiError(0, "network", "You appear to be offline. Check your connection and try again.", {});
  }
  if (res.status === 204) return undefined as T;
  const json = (await res.json().catch(() => ({}))) as Record<string, unknown>;
  if (!res.ok) {
    const err = (json.error ?? {}) as { code?: string; message?: string };
    if (res.status === 401) void supabase.auth.signOut();
    throw new ApiError(res.status, err.code ?? "error", err.message ?? `Request failed (${res.status})`, json);
  }
  return json as T;
}

export interface CreateMemoryInput {
  type: MemoryType;
  title?: string;
  content?: string;
  source_url?: string;
  mime_type?: string;
  file_size?: number;
  content_hash?: string;
  client_id?: string;
  captured_at?: string;
  metadata?: Record<string, unknown>;
  on_duplicate?: "ask" | "keep_both" | "replace";
  replace_id?: string;
}

export interface UploadTarget {
  url: string;
  method: string;
  path: string;
}

export const api = {
  me: () => request<{ id: string; profile: Profile; usage: Usage }>("GET", "/v1/me"),
  updateMe: (p: { name?: string; avatar_url?: string }) =>
    request<{ id: string; profile: Profile; usage: Usage }>("PATCH", "/v1/me", p),
  deleteMyData: (deleteAccount: boolean) =>
    request<{ deleted: boolean }>("DELETE", "/v1/me/data", { confirm: "DELETE MY DATA", delete_account: deleteAccount }),

  createMemory: (input: CreateMemoryInput) =>
    request<{ memory: Memory; upload?: UploadTarget }>("POST", "/v1/memories", input),
  uploadComplete: (id: string) => request<{ memory: Memory }>("POST", `/v1/memories/${id}/upload-complete`),
  uploadURL: (id: string) => request<UploadTarget>("POST", `/v1/memories/${id}/upload-url`),
  listMemories: (params: { type?: string; before?: string; limit?: number; category?: string; tag?: string } = {}) => {
    const q = new URLSearchParams();
    Object.entries(params).forEach(([k, v]) => v !== undefined && v !== "" && q.set(k, String(v)));
    return request<{ memories: Memory[]; next_cursor: string }>("GET", `/v1/memories?${q}`);
  },
  getMemory: (id: string) => request<{ memory: Memory }>("GET", `/v1/memories/${id}`),
  updateMemory: (
    id: string,
    patch: Partial<Pick<Memory, "title" | "summary" | "content" | "category" | "tags" | "type" | "captured_at">>,
  ) => request<{ memory: Memory }>("PATCH", `/v1/memories/${id}`, patch),
  deleteMemory: (id: string) => request<void>("DELETE", `/v1/memories/${id}`),
  retryMemory: (id: string) => request<{ memory: Memory }>("POST", `/v1/memories/${id}/retry`),
  related: (id: string) =>
    request<{ related: { memory: Memory; similarity: number }[] }>("GET", `/v1/memories/${id}/related`),
  download: (id: string) => request<{ url: string }>("GET", `/v1/memories/${id}/download`),
  checkDuplicate: (hash: string) =>
    request<{ duplicates: Memory[] }>("POST", "/v1/memories/check-duplicate", { content_hash: hash }),

  createShare: (id: string, hours: number) =>
    request<Share>("POST", `/v1/memories/${id}/shares`, { expires_in_hours: hours }),
  listShares: (id: string) => request<{ shares: Share[] }>("GET", `/v1/memories/${id}/shares`),
  revokeShare: (shareId: string) => request<void>("DELETE", `/v1/shares/${shareId}`),
  publicShare: (token: string) =>
    request<{ memory: Partial<Memory>; expires_at: string }>("GET", `/public/shares/${encodeURIComponent(token)}`, undefined, false),

  timeline: (before?: string) =>
    request<{ groups: { month: string; label: string; memories: Memory[] }[]; next_cursor: string }>(
      "GET",
      `/v1/timeline${before ? `?before=${encodeURIComponent(before)}` : ""}`,
    ),
  facets: () => request<{ types: Facet[]; categories: Facet[]; tags: Facet[] }>("GET", "/v1/facets"),
  search: (query: string, filters: SearchFilters = {}) =>
    request<{ query: string; results: SearchHit[]; total: number }>("POST", "/v1/search", { query, ...filters }),
  ask: (question: string, conversationId?: string, memoryIds?: string[]) =>
    request<AskResponse>("POST", "/v1/ask", {
      question,
      conversation_id: conversationId,
      memory_ids: memoryIds?.length ? memoryIds : undefined,
    }),
  conversations: () => request<{ conversations: Conversation[] }>("GET", "/v1/conversations"),
  conversation: (id: string) =>
    request<{ conversation: Conversation; messages: ChatMessage[] }>("GET", `/v1/conversations/${id}`),
  deleteConversation: (id: string) => request<void>("DELETE", `/v1/conversations/${id}`),
};
