export type MemoryType = "photo" | "screenshot" | "pdf" | "document" | "receipt" | "voice" | "note" | "link";
export type MemoryStatus = "pending" | "processing" | "ready" | "failed";

export interface Memory {
  id: string;
  type: MemoryType;
  status: MemoryStatus;
  title: string;
  content?: string;
  summary: string;
  category: string;
  tags: string[];
  source_url?: string;
  mime_type?: string;
  file_size: number;
  content_hash?: string;
  client_id?: string;
  captured_at?: string;
  metadata: Record<string, unknown>;
  user_edited: string[];
  processing_error?: string;
  created_at: string;
  updated_at: string;
  has_file: boolean;
  thumbnail_url?: string;
  file_url?: string;
}

export interface SearchHit {
  memory: Memory;
  score: number;
  similarity?: number;
  snippet?: string;
  matched_by: string[];
}

export interface SearchFilters {
  types?: MemoryType[];
  category?: string;
  tags?: string[];
  from?: string;
  to?: string;
}

export interface AskResponse {
  conversation_id: string;
  message_id: string;
  answer: string;
  found: boolean;
  sources: Memory[];
  considered: number;
}

export interface Conversation {
  id: string;
  title: string;
  created_at: string;
  updated_at: string;
}

export interface ChatMessage {
  id: string;
  role: "user" | "assistant";
  content: string;
  sources: Memory[];
  created_at: string;
}

export interface Usage {
  plan: "free" | "pro";
  storage_used: number;
  storage_limit: number;
  ai_queries: number;
  ai_query_limit: number;
  processed_items: number;
  processed_limit: number;
  max_file_bytes: number;
  memory_count: number;
}

export interface Profile {
  name: string;
  email: string;
  avatar_url: string;
  plan: string;
  created_at: string;
}

export interface Facet {
  name: string;
  count: number;
}

export interface Share {
  id: string;
  url?: string;
  expires_at: string;
  created_at: string;
}

export const TYPE_META: Record<MemoryType, { label: string; emoji: string; plural: string }> = {
  photo: { label: "Photo", emoji: "📸", plural: "Photos" },
  screenshot: { label: "Screenshot", emoji: "📱", plural: "Screenshots" },
  pdf: { label: "PDF", emoji: "📄", plural: "PDFs" },
  document: { label: "Document", emoji: "📄", plural: "Documents" },
  receipt: { label: "Receipt", emoji: "🧾", plural: "Receipts" },
  voice: { label: "Voice note", emoji: "🎙️", plural: "Voice" },
  note: { label: "Note", emoji: "📝", plural: "Notes" },
  link: { label: "Link", emoji: "🔗", plural: "Links" },
};
