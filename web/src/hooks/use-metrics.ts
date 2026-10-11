import { useCallback, useEffect, useState } from "react";

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

async function getData<T>(path: string): Promise<T> {
  const res = await fetch(`${base}${path}`, { credentials: "include" });
  if (!res.ok) throw await readApiError(res);
  const body = (await res.json()) as { data: T };
  return body.data;
}

// usePolled fetches path now, then every METRICS_REFRESH_MS while the tab
// is visible (and once more when it becomes visible again). A null path
// (nothing selected yet) fetches nothing. Metrics are never pushed over SSE.
export function usePolled<T>(path: string | null): MetricsState<T> {
  const [data, setData] = useState<T | null>(null);
  const [loading, setLoading] = useState(path !== null);
  const [error, setError] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    if (!path) return;
    try {
      const next = await getData<T>(path);
      setData(next);
      setError(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Failed to load metrics");
    } finally {
      setLoading(false);
    }
  }, [path]);

  useEffect(() => {
    if (!path) return;
    void refresh();
    const tick = () => {
      if (document.visibilityState === "visible") void refresh();
    };
    const id = window.setInterval(tick, METRICS_REFRESH_MS);
    document.addEventListener("visibilitychange", tick);
    return () => {
      window.clearInterval(id);
      document.removeEventListener("visibilitychange", tick);
    };
  }, [path, refresh]);

  return { data, loading, error, refresh };
}

function query(params: Record<string, string>): string {
  return new URLSearchParams(params).toString();
}

// useServerMetrics loads GET /api/v1/metrics/server for one server.
export function useServerMetrics(serverId: string | null, range: MetricsRange) {
  return usePolled<ServerSeries>(
    serverId ? `/api/v1/metrics/server?${query({ server_id: serverId, range })}` : null,
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
  );
}

// useMetricsOverview loads the dashboard overview of every server.
export function useMetricsOverview() {
  return usePolled<{ servers: ServerOverview[] | null }>("/api/v1/metrics/overview");
}
