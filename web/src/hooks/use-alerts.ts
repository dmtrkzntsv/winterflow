import { useContext, useEffect, useSyncExternalStore } from "react";

import { apiBaseUrl } from "@/config";
import { NotificationsContext } from "@/context/notifications-context-base";
import { METRICS_FETCH_TIMEOUT_MS, METRICS_REFRESH_MS } from "@/hooks/use-metrics";
import { readApiError } from "@/lib/api";
import { ALERT_NOTIFICATION, type ActiveAlert } from "@/lib/alerts";

const base = apiBaseUrl.endsWith("/") ? apiBaseUrl.slice(0, -1) : apiBaseUrl;

// alertsGet and alertsPost call /api/v1/alerts/* and unwrap the envelope;
// a failed call throws the server's message.
export async function alertsGet<T>(path: string, params?: Record<string, string>, signal?: AbortSignal): Promise<T> {
  const qs = params ? `?${new URLSearchParams(params).toString()}` : "";
  const res = await fetch(`${base}/api/v1/alerts/${path}${qs}`, { credentials: "include", signal });
  if (!res.ok) throw await readApiError(res);
  return ((await res.json()) as { data: T }).data;
}

export async function alertsPost<T>(path: string, body: unknown): Promise<T> {
  const res = await fetch(`${base}/api/v1/alerts/${path}`, {
    method: "POST",
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!res.ok) throw await readApiError(res);
  return ((await res.json()) as { data: T }).data;
}

type ActiveSnapshot = {
  data: ActiveAlert[] | null;
  loading: boolean;
  error: string | null;
  // updatedAt is when data last arrived (ms).
  updatedAt: number | null;
};

const EMPTY: ActiveSnapshot = { data: null, loading: true, error: null, updatedAt: null };

type ActiveStore = {
  snapshot: ActiveSnapshot;
  subscribe: (listener: () => void) => () => void;
  refresh: () => Promise<void>;
};

// One poller per distinct query, shared by every component reading it (the
// Monitoring page's banner and its rules section read the same list): the
// poll runs while at least one of them is mounted, and a last unmount
// stops it and forgets the store.
const stores = new Map<string, ActiveStore>();

// alertStoreCount is how many queries have a store (tests: none is kept
// once its last reader is gone).
export function alertStoreCount(): number {
  return stores.size;
}

function createStore(qs: string, params: Record<string, string>): ActiveStore {
  const listeners = new Set<() => void>();
  let inflight: AbortController | null = null;
  let timer: number | null = null;
  let queued: Promise<void> | null = null;

  const set = (patch: Partial<ActiveSnapshot>) => {
    store.snapshot = { ...store.snapshot, ...patch };
    listeners.forEach((l) => l());
  };

  const load = async () => {
    inflight?.abort();
    const ctl = new AbortController();
    inflight = ctl;
    const deadline = window.setTimeout(() => ctl.abort(), METRICS_FETCH_TIMEOUT_MS);
    try {
      const next = await alertsGet<ActiveAlert[] | null>("get-active", params, ctl.signal);
      if (inflight !== ctl) return;
      set({ data: next ?? [], updatedAt: Date.now(), error: null });
    } catch (e) {
      if (inflight !== ctl) return;
      set({ error: e instanceof Error ? e.message : "Failed to load alerts" });
    } finally {
      window.clearTimeout(deadline);
      if (inflight === ctl) set({ loading: false });
    }
  };

  const tick = () => {
    if (document.visibilityState === "visible") void load();
  };

  const start = () => {
    void load();
    timer = window.setInterval(tick, METRICS_REFRESH_MS);
    document.addEventListener("visibilitychange", tick);
  };

  const stop = () => {
    inflight?.abort();
    inflight = null;
    if (timer !== null) window.clearInterval(timer);
    timer = null;
    document.removeEventListener("visibilitychange", tick);
    queued = null;
    store.snapshot = EMPTY;
    if (stores.get(qs) === store) stores.delete(qs);
  };

  const store: ActiveStore = {
    snapshot: EMPTY,
    subscribe: (listener) => {
      listeners.add(listener);
      // A reader that resubscribes (a re-run effect) brings the store back.
      if (!stores.has(qs)) stores.set(qs, store);
      if (listeners.size === 1) start();
      return () => {
        listeners.delete(listener);
        if (listeners.size === 0) stop();
      };
    },
    // Several readers asking in the same tick (one SSE notification reaches
    // all of them) make one read.
    refresh: () => {
      queued ??= Promise.resolve().then(() => {
        queued = null;
        return listeners.size > 0 ? load() : undefined;
      });
      return queued;
    },
  };
  return store;
}

function storeFor(qs: string, params: Record<string, string>): ActiveStore {
  let store = stores.get(qs);
  if (!store) {
    store = createStore(qs, params);
    stores.set(qs, store);
  }
  return store;
}

// useActiveAlerts polls the firing alerts (all of the org's, or one
// server's or app's) every minute while visible, and refreshes at once on
// an SSE "alert" transition. Readers of the same query share one poller.
// Outside a NotificationsProvider it only polls.
export function useActiveAlerts(serverId?: string | null, appId?: string | null) {
  const params: Record<string, string> = {};
  if (serverId) params.server_id = serverId;
  if (serverId && appId) params.app_id = appId;
  const qs = new URLSearchParams(params).toString();
  const store = storeFor(qs, params);
  const state = useSyncExternalStore(store.subscribe, () => store.snapshot);
  const subscribe = useContext(NotificationsContext)?.subscribe;
  const { refresh } = store;
  useEffect(() => {
    if (!subscribe) return;
    return subscribe((n) => {
      if (n.type === ALERT_NOTIFICATION) void refresh();
    });
  }, [subscribe, refresh]);
  return { ...state, data: state.data ?? [], refresh };
}
