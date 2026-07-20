import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";

import { useAuth } from "@/context/use-auth";
import { apiBaseUrl } from "@/config";

import {
  NotificationsContext,
  type Notification,
  type NotificationHandler,
} from "./notifications-context-base";

const base = apiBaseUrl.endsWith("/") ? apiBaseUrl.slice(0, -1) : apiBaseUrl;
const streamUrl = `${base}/api/v1/sse`;

// NotificationsProvider holds a single EventSource to the API's SSE stream and
// fans every incoming notification out to subscribers. The backend delivers
// async command results (Ref = request_id) and unsolicited status/changed
// events here; consumers (e.g. AppsProvider) subscribe to react to them.
// Results can arrive on the SSE stream BEFORE the caller has parsed its 202
// response to learn the request_id (the agent answers fast commands in ~1ms).
// Recent correlated notifications are kept briefly so waitFor can resolve from
// this buffer instead of missing an event it wasn't yet subscribed for.
const REPLAY_TTL_MS = 30000;
const REPLAY_MAX = 100;

export function NotificationsProvider({ children }: { children: ReactNode }) {
  const { isAuthenticated } = useAuth();
  const [connected, setConnected] = useState(false);
  const handlersRef = useRef<Set<NotificationHandler>>(new Set());
  const replayRef = useRef<{ n: Notification; at: number }[]>([]);

  useEffect(() => {
    // When not authenticated we open no stream; `connected` stays/returns false
    // via this effect's cleanup, so no synchronous setState is needed here.
    if (!isAuthenticated) return;

    // withCredentials carries the auth cookie on same-origin connections.
    const es = new EventSource(streamUrl, { withCredentials: true });

    es.onopen = () => setConnected(true);
    es.onerror = () => {
      // The browser auto-reconnects EventSource; just reflect the dropped state.
      setConnected(false);
    };
    es.onmessage = (event) => {
      let parsed: Notification;
      try {
        parsed = JSON.parse(event.data) as Notification;
      } catch {
        return;
      }
      // Buffer correlated results (unsolicited events have no ref) for
      // waitFor callers that lost the 202-vs-SSE race.
      if (parsed.ref) {
        const now = Date.now();
        const kept = replayRef.current.filter((e) => now - e.at < REPLAY_TTL_MS);
        kept.push({ n: parsed, at: now });
        replayRef.current = kept.slice(-REPLAY_MAX);
      }
      handlersRef.current.forEach((handler) => {
        try {
          handler(parsed);
        } catch {
          // A faulty subscriber must not break delivery to the others.
        }
      });
    };

    return () => {
      es.close();
      setConnected(false);
    };
  }, [isAuthenticated]);

  const value = useMemo(() => {
    const subscribe = (handler: NotificationHandler) => {
      handlersRef.current.add(handler);
      return () => {
        handlersRef.current.delete(handler);
      };
    };
    return {
      connected,
      subscribe,
      waitFor(requestId: string, timeoutMs = 60000) {
        return new Promise<Notification>((resolve, reject) => {
          // The result may already have arrived while the 202 response was
          // still being read — check the replay buffer before subscribing.
          const now = Date.now();
          const early = replayRef.current.find(
            (e) => e.n.ref === requestId && now - e.at < REPLAY_TTL_MS,
          );
          if (early) {
            resolve(early.n);
            return;
          }
          const unsubscribe = subscribe((n) => {
            if (n.ref !== requestId) return;
            clearTimeout(timer);
            unsubscribe();
            resolve(n);
          });
          const timer = setTimeout(() => {
            unsubscribe();
            reject(new Error("Timed out waiting for command result"));
          }, timeoutMs);
        });
      },
    };
  }, [connected]);

  return (
    <NotificationsContext.Provider value={value}>
      {children}
    </NotificationsContext.Provider>
  );
}
