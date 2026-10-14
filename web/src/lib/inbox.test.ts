import { afterEach, describe, expect, it, vi } from "vitest";

import type { AlertEvent } from "@/lib/alerts";
import {
  countUnread,
  inboxSeenKey,
  readInboxSeen,
  subscribeInboxSeen,
  unreadBadge,
  writeInboxSeen,
} from "@/lib/inbox";

describe("inbox last-seen store", () => {
  afterEach(() => localStorage.clear());

  it("keeps one timestamp per user", () => {
    writeInboxSeen("u1", 1000);
    expect(localStorage.getItem(inboxSeenKey("u1"))).toBe("1000");
    expect(readInboxSeen("u1")).toBe(1000);
    expect(readInboxSeen("u2")).toBeNull();
  });

  it("reads garbage as never seen", () => {
    localStorage.setItem(inboxSeenKey("u1"), "yesterday");
    expect(readInboxSeen("u1")).toBeNull();
  });

  it("tells subscribers about a write here and in another tab", () => {
    const seen = vi.fn();
    const unsubscribe = subscribeInboxSeen(seen);
    writeInboxSeen("u1", 5);
    window.dispatchEvent(new StorageEvent("storage", { key: inboxSeenKey("u1") }));
    window.dispatchEvent(new StorageEvent("storage", { key: "wf.theme" }));
    expect(seen).toHaveBeenCalledTimes(2);
    window.dispatchEvent(new StorageEvent("storage", { key: null })); // localStorage.clear() elsewhere
    expect(seen).toHaveBeenCalledTimes(3);
    unsubscribe();
    writeInboxSeen("u1", 6);
    expect(seen).toHaveBeenCalledTimes(3);
  });
});

describe("countUnread", () => {
  const events: Pick<AlertEvent, "kind" | "created_at">[] = [
    { kind: "fired", created_at: "2026-10-13T11:00:00Z" },
    { kind: "fired", created_at: "2026-10-13T09:00:00Z" },
  ];

  it("counts the events newer than the last visit", () => {
    expect(countUnread(events, Date.parse("2026-10-13T10:00:00Z"))).toBe(1);
  });

  it("counts a firing only: its resolution is no news", () => {
    const incident: typeof events = [...events, { kind: "resolved", created_at: "2026-10-13T11:30:00Z" }];
    expect(countUnread(incident, Date.parse("2026-10-13T10:00:00Z"))).toBe(1);
  });

  it("counts nothing before the first visit is recorded", () => {
    expect(countUnread(events, null)).toBe(0);
  });
});

describe("unreadBadge", () => {
  it("caps the badge at 99+", () => {
    expect(unreadBadge(3)).toBe("3");
    expect(unreadBadge(99)).toBe("99");
    expect(unreadBadge(100)).toBe("99+");
  });
});
