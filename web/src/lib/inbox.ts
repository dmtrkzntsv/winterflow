import type { AlertEvent } from "@/lib/alerts";

// The Inbox's read state lives in this browser (spec §5: no backend
// change): per user, the time of their last "Mark all as read" (or of
// their first visit). Events newer than it are unread.
export const INBOX_SEEN_PREFIX = "wf.inbox.seen.";

const listeners = new Set<() => void>();

export function inboxSeenKey(userId: string): string {
  return `${INBOX_SEEN_PREFIX}${userId}`;
}

// readInboxSeen is the user's last-seen time (ms), null when never
// recorded or unreadable.
export function readInboxSeen(userId: string): number | null {
  try {
    const raw = window.localStorage.getItem(inboxSeenKey(userId));
    if (raw === null) return null;
    const at = Number(raw);
    return Number.isFinite(at) ? at : null;
  } catch {
    return null;
  }
}

export function writeInboxSeen(userId: string, at: number): void {
  try {
    window.localStorage.setItem(inboxSeenKey(userId), String(at));
  } catch {
    // Storage blocked: nothing counts as unread (readInboxSeen stays null).
  }
  listeners.forEach((l) => l());
}

// subscribeInboxSeen calls onChange after a write here or in another tab.
export function subscribeInboxSeen(onChange: () => void): () => void {
  listeners.add(onChange);
  const onStorage = (event: StorageEvent) => {
    // key === null: localStorage.clear() in another tab.
    if (event.key === null || event.key.startsWith(INBOX_SEEN_PREFIX)) onChange();
  };
  window.addEventListener("storage", onStorage);
  return () => {
    listeners.delete(onChange);
    window.removeEventListener("storage", onStorage);
  };
}

// countUnread counts the alerts that fired after seenAt (none before the
// first visit is recorded). A resolution is no news: one incident counts
// once, not twice.
export function countUnread(events: Pick<AlertEvent, "kind" | "created_at">[], seenAt: number | null): number {
  if (seenAt === null) return 0;
  return events.filter((e) => e.kind === "fired" && Date.parse(e.created_at) > seenAt).length;
}

// unreadBadge is the sidebar badge text.
export function unreadBadge(count: number): string {
  return count > 99 ? "99+" : String(count);
}
