import { describe, expect, it } from "vitest";
import { bytes, timeAgo } from "./format";

describe("timeAgo", () => {
  const now = new Date("2026-10-01T12:00:00Z").getTime();
  it("handles recent times", () => {
    expect(timeAgo("2026-10-01T11:59:50Z", now)).toBe("just now");
    expect(timeAgo("2026-10-01T11:58:00Z", now)).toBe("2 min ago");
  });
  it("handles yesterday", () => {
    expect(timeAgo(new Date(now - 86_400_000).toISOString(), now)).toBe("Yesterday");
  });
});

describe("bytes", () => {
  it("formats sizes", () => {
    expect(bytes(512)).toBe("512 B");
    expect(bytes(1536)).toBe("1.5 KB");
    expect(bytes(25 * 1024 * 1024)).toBe("25 MB");
  });
});
