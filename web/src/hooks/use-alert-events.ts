import { useCallback, useContext, useEffect, useRef, useState } from "react";

import { NotificationsContext } from "@/context/notifications-context-base";
import { alertsGet } from "@/hooks/use-alerts";
import { ALERT_NOTIFICATION, type AlertEvent } from "@/lib/alerts";

type Page = { events: AlertEvent[] | null; next_cursor: string };

// EventKindFilter is the Inbox's All / Firing / Resolved filter ("fired" is
// the API's kind of a firing transition).
export type EventKindFilter = "all" | "fired" | "resolved";

export type AlertEventsState = {
  events: AlertEvent[];
  hasMore: boolean;
  loading: boolean;
  error: string | null;
  loadMore: () => void;
};

function eventParams(kind: EventKindFilter, serverId: string | null, cursor: string): Record<string, string> {
  const params: Record<string, string> = {};
  if (kind !== "all") params.kind = kind;
  if (serverId) params.server_id = serverId;
  if (cursor) params.cursor = cursor;
  return params;
}

// useAlertEvents is the org's alert history, newest first, filtered by kind
// and server and paged by the API's cursor. A filter change starts over. A
// response to an older request (filters changed meanwhile) is dropped
// instead of overwriting the newer list. While mounted, every "alert"
// notification reads the first page again and puts the events not shown
// yet on top. The loaded pages and the cursor stay, and there is no polling.
export function useAlertEvents(kind: EventKindFilter, serverId: string | null): AlertEventsState {
  const [events, setEvents] = useState<AlertEvent[]>([]);
  const [cursor, setCursor] = useState("");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  // latest numbers page requests: only the newest may land.
  const latest = useRef(0);
  // generation numbers first-page loads: a live update read before the
  // filters changed is dropped.
  const generation = useRef(0);

  const load = useCallback(
    async (after: string) => {
      const request = ++latest.current;
      if (!after) {
        // A first page replaces the list: the previous filter's events (and
        // its cursor) go at once, so they never show under the new filter,
        // not even when this read fails.
        generation.current++;
        setEvents([]);
        setCursor("");
      }
      setLoading(true);
      try {
        const page = await alertsGet<Page>("get-events", eventParams(kind, serverId, after));
        if (request !== latest.current) return;
        const got = page.events ?? [];
        setEvents((prev) => (after ? [...prev, ...got] : got));
        setCursor(page.next_cursor);
        setError(null);
      } catch (e) {
        if (request !== latest.current) return;
        setError(e instanceof Error ? e.message : "Failed to load alerts");
      } finally {
        if (request === latest.current) setLoading(false);
      }
    },
    [kind, serverId],
  );

  useEffect(() => {
    void load("");
  }, [load]);

  const mergeNewest = useCallback(async () => {
    const gen = generation.current;
    try {
      const page = await alertsGet<Page>("get-events", eventParams(kind, serverId, ""));
      if (gen !== generation.current) return;
      setEvents((prev) => {
        const known = new Set(prev.map((e) => e.id));
        const fresh = (page.events ?? []).filter((e) => !known.has(e.id));
        return fresh.length > 0 ? [...fresh, ...prev] : prev;
      });
    } catch {
      // A live update is best effort: the list stays as it is.
    }
  }, [kind, serverId]);

  const subscribe = useContext(NotificationsContext)?.subscribe;
  useEffect(() => {
    if (!subscribe) return;
    return subscribe((n) => {
      if (n.type === ALERT_NOTIFICATION) void mergeNewest();
    });
  }, [subscribe, mergeNewest]);

  const loadMore = useCallback(() => {
    if (cursor) void load(cursor);
  }, [cursor, load]);

  return { events, hasMore: cursor !== "", loading, error, loadMore };
}
