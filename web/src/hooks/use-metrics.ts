import { useCallback, useEffect, useRef, useState } from "react";

import { apiBaseUrl } from "@/config";
import { readApiError } from "@/lib/api";
import type {
  MetricsRange,
  Series,
  ServerOverview,
  ServerSeries,
} from "@/lib/metrics";

const base = apiBaseUrl.endsWith("/") ? apiBaseUrl.slice(0, -1) : apiBaseUrl;

// METRICS_REFRESH_MS matches the agent's sampling period: refreshing faster
// would only refetch the same points.
export const METRICS_REFRESH_MS = 60_000;

export type MetricsState<T> = {
  data: T | null;
  loading: boolean;
  error: string | null;
  refresh: () => Promise<void>;
};

// METRICS_FETCH_TIMEOUT_MS bounds one request so a hung connection cannot
// pin the loading state or pile up behind the next tick.
export const METRICS_FETCH_TIMEOUT_MS = 15_000;

async function getData<T>(path: string, signal: AbortSignal): Promise<T> {
  const res = await fetch(`${base}${path}`, { credentials: "include", signal });
  if (!res.ok) throw await readApiError(res);
  const body = (await res.json()) as { data: T };
  return body.data;
}

// usePolled fetches path now, then every METRICS_REFRESH_MS while the tab
// is visible (and once more when it becomes visible again). A null path
// (nothing selected yet) fetches nothing. Metrics are never pushed over SSE.
// Results of a superseded path or an unmounted hook are dropped. normalize
// (a stable function) maps the payload at the boundary.
export function usePolled<T>(
  path: string | null,
  normalize?: (data: T) => T,
): MetricsState<T> {
  const [data, setData] = useState<T | null>(null);
  const [loading, setLoading] = useState(path !== null);
  const [error, setError] = useState<string | null>(null);
  const [prevPath, setPrevPath] = useState(path);
  const loadRef = useRef<() => Promise<void>>(async () => {});

  // A new path starts from scratch instead of showing the old one's data.
  if (prevPath !== path) {
    setPrevPath(path);
    setData(null);
    setError(null);
    setLoading(path !== null);
  }

  useEffect(() => {
    if (!path) {
      loadRef.current = async () => {};
      return;
    }
    let active = true;
    let inflight: AbortController | null = null;

    const load = async () => {
      inflight?.abort();
      const ctl = new AbortController();
      inflight = ctl;
      const timer = window.setTimeout(() => ctl.abort(), METRICS_FETCH_TIMEOUT_MS);
      try {
        const next = await getData<T>(path, ctl.signal);
        if (!active || inflight !== ctl) return;
        setData(normalize ? normalize(next) : next);
        setError(null);
      } catch (e) {
        if (!active || inflight !== ctl) return;
        setError(e instanceof Error ? e.message : "Failed to load metrics");
      } finally {
        window.clearTimeout(timer);
        if (active && inflight === ctl) setLoading(false);
      }
    };
    loadRef.current = load;

    void load();
    const tick = () => {
      if (document.visibilityState === "visible") void load();
    };
    const id = window.setInterval(tick, METRICS_REFRESH_MS);
    document.addEventListener("visibilitychange", tick);
    return () => {
      active = false;
      inflight?.abort();
      window.clearInterval(id);
      document.removeEventListener("visibilitychange", tick);
    };
  }, [path, normalize]);

  const refresh = useCallback(() => loadRef.current(), []);

  return { data, loading, error, refresh };
}

// Go may encode a nil slice as null; consumers always see arrays.
function normalizeSeries(s: Series): Series {
  return { ...s, points: s.points ?? [] };
}

function normalizeServerSeries(s: ServerSeries): ServerSeries {
  return {
    ...s,
    points: s.points ?? [],
    apps: (s.apps ?? []).map((a) => ({ ...a, points: a.points ?? [] })),
  };
}

type Overview = { servers: ServerOverview[] | null };

function normalizeOverview(o: Overview): Overview {
  return {
    servers: (o.servers ?? []).map((sv) => ({ ...sv, sparkline: sv.sparkline ?? [] })),
  };
}

function query(params: Record<string, string>): string {
  return new URLSearchParams(params).toString();
}

// useServerMetrics loads GET /api/v1/metrics/server for one server.
export function useServerMetrics(serverId: string | null, range: MetricsRange) {
  return usePolled<ServerSeries>(
    serverId ? `/api/v1/metrics/server?${query({ server_id: serverId, range })}` : null,
    normalizeServerSeries,
  );
}

// useAppMetrics loads GET /api/v1/metrics/app for one app.
export function useAppMetrics(
  serverId: string | null,
  appId: string | null,
  range: MetricsRange,
) {
  return usePolled<Series>(
    serverId && appId
      ? `/api/v1/metrics/app?${query({ server_id: serverId, app_id: appId, range })}`
      : null,
    normalizeSeries,
  );
}

// useMetricsOverview loads the dashboard overview of every server.
export function useMetricsOverview() {
  return usePolled<{ servers: ServerOverview[] | null }>("/api/v1/metrics/overview", normalizeOverview);
}
