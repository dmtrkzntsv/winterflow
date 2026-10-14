import { useCallback, useContext, useEffect, useMemo, useState, useSyncExternalStore } from "react";

import { NotificationsContext } from "@/context/notifications-context-base";
import { ProfileContext } from "@/context/profile-context-base";
import { alertsGet } from "@/hooks/use-alerts";
import { ALERT_NOTIFICATION, type AlertEvent } from "@/lib/alerts";
import { countUnread, readInboxSeen, subscribeInboxSeen, writeInboxSeen } from "@/lib/inbox";

// How many of the newest events the unread count looks at (the badge says
// 99+ above 99 anyway).
export const UNREAD_SCAN = 100;

type EventsPage = { events: AlertEvent[] | null; next_cursor: string };

export type InboxUnread = {
  count: number;
  // seenAt is the user's last-seen time (ms), null before it is recorded.
  seenAt: number | null;
  isUnread: (createdAt: string) => boolean;
  // markAllRead moves last-seen to now, or past the newest event when the
  // server's clock is ahead of this browser's.
  markAllRead: () => void;
};

// useInboxUnread is the signed-in user's unread Inbox count. It reads the
// newest events when mounted and again on every alert notification (no
// polling: idle cost stays zero). A first visit starts counting from now,
// so an install's old history isn't "unread". Outside the providers (or
// signed out) it counts nothing.
export function useInboxUnread(): InboxUnread {
  const userId = useContext(ProfileContext)?.profile?.user_id ?? null;
  const subscribe = useContext(NotificationsContext)?.subscribe;
  const seenAt = useSyncExternalStore(
    subscribeInboxSeen,
    () => (userId ? readInboxSeen(userId) : null),
    () => null,
  );
  // The loaded events remember whose they are: another user's are not shown
  // (nor counted) while this one's load.
  const [loaded, setLoaded] = useState<{ userId: string; events: AlertEvent[] } | null>(null);
  const events = useMemo(
    () => (loaded && loaded.userId === userId ? loaded.events : []),
    [loaded, userId],
  );
  const [version, setVersion] = useState(0);

  useEffect(() => {
    if (userId && readInboxSeen(userId) === null) writeInboxSeen(userId, Date.now());
  }, [userId]);

  useEffect(() => {
    if (!userId) return;
    let active = true;
    alertsGet<EventsPage>("get-events", { limit: String(UNREAD_SCAN) })
      .then((page) => {
        if (active) setLoaded({ userId, events: page.events ?? [] });
      })
      .catch(() => {
        // The badge is best effort: keep the last count.
      });
    return () => {
      active = false;
    };
  }, [userId, version]);

  useEffect(() => {
    if (!subscribe) return;
    return subscribe((n) => {
      if (n.type === ALERT_NOTIFICATION) setVersion((v) => v + 1);
    });
  }, [subscribe]);

  const isUnread = useCallback(
    (createdAt: string) => seenAt !== null && Date.parse(createdAt) > seenAt,
    [seenAt],
  );
  const markAllRead = useCallback(() => {
    if (!userId) return;
    const newest = events.map((e) => Date.parse(e.created_at)).filter(Number.isFinite);
    writeInboxSeen(userId, Math.max(Date.now(), ...newest));
  }, [userId, events]);

  return { count: countUnread(events, seenAt), seenAt, isUnread, markAllRead };
}
