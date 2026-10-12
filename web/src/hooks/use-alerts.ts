import { useContext, useEffect } from "react";

import { apiBaseUrl } from "@/config";
import { NotificationsContext } from "@/context/notifications-context-base";
import { usePolled } from "@/hooks/use-metrics";
import { readApiError } from "@/lib/api";
import { ALERT_NOTIFICATION, type ActiveAlert } from "@/lib/alerts";

const base = apiBaseUrl.endsWith("/") ? apiBaseUrl.slice(0, -1) : apiBaseUrl;

// alertsGet and alertsPost call /api/v1/alerts/* and unwrap the envelope;
// a failed call throws the server's message.
export async function alertsGet<T>(path: string, params?: Record<string, string>): Promise<T> {
  const qs = params ? `?${new URLSearchParams(params).toString()}` : "";
  const res = await fetch(`${base}/api/v1/alerts/${path}${qs}`, { credentials: "include" });
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

function normalizeActive(list: ActiveAlert[] | null): ActiveAlert[] {
  return list ?? [];
}

// useActiveAlerts polls the firing alerts (all of the org's, or one
// server's or app's) every minute while visible, and refreshes at once on
// an SSE "alert" transition. Outside a NotificationsProvider it only polls.
export function useActiveAlerts(serverId?: string | null, appId?: string | null) {
  const params = new URLSearchParams();
  if (serverId) params.set("server_id", serverId);
  if (serverId && appId) params.set("app_id", appId);
  const qs = params.toString();
  const state = usePolled<ActiveAlert[] | null>(`/api/v1/alerts/get-active${qs ? `?${qs}` : ""}`, normalizeActive);
  const subscribe = useContext(NotificationsContext)?.subscribe;
  const { refresh } = state;
  useEffect(() => {
    if (!subscribe) return;
    return subscribe((n) => {
      if (n.type === ALERT_NOTIFICATION) void refresh();
    });
  }, [subscribe, refresh]);
  return { ...state, data: state.data ?? [] };
}
