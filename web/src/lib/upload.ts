import { api, ApiError, type CreateMemoryInput } from "./api";
import type { Memory, MemoryType } from "./types";

export const DOCUMENT_ACCEPT =
  ".pdf,.docx,.txt,.md,.csv,.html,.htm,.rtf,.json,application/pdf,application/vnd.openxmlformats-officedocument.wordprocessingml.document,text/plain,text/markdown,text/csv,text/html,application/rtf";

const EXT_MIME: Record<string, string> = {
  pdf: "application/pdf",
  docx: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
  txt: "text/plain",
  md: "text/markdown",
  csv: "text/csv",
  html: "text/html",
  htm: "text/html",
  rtf: "application/rtf",
  json: "application/json",
};

export function mimeOf(file: File): string {
  if (file.type) return file.type;
  const ext = file.name.split(".").pop()?.toLowerCase() ?? "";
  return EXT_MIME[ext] ?? "application/octet-stream";
}

export async function sha256(data: Blob | string): Promise<string> {
  const buf = typeof data === "string" ? new TextEncoder().encode(data) : await data.arrayBuffer();
  const digest = await crypto.subtle.digest("SHA-256", buf);
  return Array.from(new Uint8Array(digest), (b) => b.toString(16).padStart(2, "0")).join("");
}

/** Downscale large photos to JPEG on the client (the cloud does the heavy work). */
export async function compressImage(file: File, maxSide = 2048, quality = 0.85): Promise<Blob> {
  if (!file.type.startsWith("image/") || file.type === "image/gif") return file;
  try {
    const bitmap = await createImageBitmap(file);
    const scale = Math.min(1, maxSide / Math.max(bitmap.width, bitmap.height));
    if (scale === 1 && file.type === "image/jpeg" && file.size < 3_000_000) return file;
    const canvas = document.createElement("canvas");
    canvas.width = Math.round(bitmap.width * scale);
    canvas.height = Math.round(bitmap.height * scale);
    canvas.getContext("2d")!.drawImage(bitmap, 0, 0, canvas.width, canvas.height);
    const blob = await new Promise<Blob | null>((r) => canvas.toBlob(r, "image/jpeg", quality));
    return blob && blob.size < file.size ? blob : file;
  } catch {
    return file; // e.g. HEIC in a browser that can't decode it; the server will explain.
  }
}

export class DuplicateError extends Error {
  duplicates: Memory[];
  retry: (choice: "keep_both" | "replace", replaceId?: string) => Promise<Memory>;
  constructor(duplicates: Memory[], retry: DuplicateError["retry"]) {
    super("This looks similar to an existing memory.");
    this.duplicates = duplicates;
    this.retry = retry;
  }
}

async function putFile(url: string, blob: Blob, mime: string, onProgress?: (p: number) => void) {
  await new Promise<void>((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open("PUT", url);
    xhr.setRequestHeader("Content-Type", mime);
    xhr.setRequestHeader("x-upsert", "true");
    xhr.upload.onprogress = (e) => e.lengthComputable && onProgress?.(e.loaded / e.total);
    xhr.onload = () =>
      xhr.status < 300 ? resolve() : reject(new ApiError(xhr.status, "upload_failed", "Upload failed. Please retry.", {}));
    xhr.onerror = () => reject(new ApiError(0, "network", "Upload failed: you appear to be offline.", {}));
    xhr.send(blob);
  });
}

async function createWithDuplicateCheck(input: CreateMemoryInput, after: (m: Memory, upload?: { url: string }) => Promise<Memory>) {
  const attempt = async (extra: Partial<CreateMemoryInput>) => {
    const res = await api.createMemory({ ...input, ...extra });
    return after(res.memory, res.upload);
  };
  try {
    return await attempt({});
  } catch (e) {
    if (e instanceof ApiError && e.code === "duplicate") {
      throw new DuplicateError((e.body.duplicates as Memory[]) ?? [], (choice, replaceId) =>
        attempt({ on_duplicate: choice, replace_id: replaceId }),
      );
    }
    throw e;
  }
}

/** Create a file-backed memory: record → direct upload to private storage → start processing. */
export async function uploadFile(
  file: File,
  type: MemoryType,
  opts: { onProgress?: (p: number) => void; metadata?: Record<string, unknown> } = {},
): Promise<Memory> {
  const isImage = type === "photo" || type === "screenshot" || type === "receipt";
  const blob = isImage ? await compressImage(file) : file;
  const mime = isImage && blob !== file ? "image/jpeg" : mimeOf(file);
  const hash = await sha256(file); // hash the original so re-saving the same file is detected
  return createWithDuplicateCheck(
    {
      type,
      mime_type: mime,
      file_size: blob.size,
      content_hash: hash,
      client_id: crypto.randomUUID(),
      captured_at: file.lastModified ? new Date(file.lastModified).toISOString() : undefined,
      metadata: { original_name: file.name, ...opts.metadata },
    },
    async (memory, upload) => {
      if (!upload) return memory;
      await putFile(upload.url, blob, mime, opts.onProgress);
      return (await api.uploadComplete(memory.id)).memory;
    },
  );
}

export function saveNote(content: string, title?: string) {
  return createWithDuplicateCheck(
    { type: "note", content, title: title || undefined, client_id: crypto.randomUUID() },
    async (m) => m,
  );
}

export function saveLink(url: string, note?: string) {
  return createWithDuplicateCheck(
    { type: "link", source_url: url, content: note || undefined, client_id: crypto.randomUUID() },
    async (m) => m,
  );
}

export function typeForFile(file: File): MemoryType {
  const mime = mimeOf(file);
  if (mime.startsWith("image/")) return /screenshot|screen shot|capture/i.test(file.name) ? "screenshot" : "photo";
  if (mime.startsWith("audio/")) return "voice";
  if (mime === "application/pdf") return "pdf";
  return "document";
}
