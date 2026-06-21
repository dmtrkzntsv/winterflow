import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";

import { useAuth } from "@/context/use-auth";
import { useServers } from "@/context/use-servers";
import { useNotifications } from "@/context/use-notifications";
import { apiBaseUrl } from "@/config";

import { AppsContext, type App } from "./apps-context-base";

type AppResponse = {
  id: string;
  server_id: string;
  name: string;
  version: string;
  icon: string;
  color: string;
  created_at: string;
};

type GetAppsResponse = {
  success: boolean;
  data?: { apps?: AppResponse[] | null };
};

type AcceptedResponse = {
  data?: { request_id?: string };
};

const base = apiBaseUrl.endsWith("/") ? apiBaseUrl.slice(0, -1) : apiBaseUrl;

const toApp = (a: AppResponse): App => ({
  id: a.id,
  serverId: a.server_id,
  name: a.name,
  version: a.version,
  icon: a.icon,
  color: a.color,
  createdAt: a.created_at,
});

export function AppsProvider({ children }: { children: ReactNode }) {
  const { isAuthenticated } = useAuth();
  const { activeServerId } = useServers();
  const { subscribe } = useNotifications();
  const [apps, setApps] = useState<App[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const mounted = useRef(false);
  // request_ids dispatched by this provider that should trigger a re-read of
  // the apps list when their result arrives over SSE.
  const pendingRefs = useRef<Set<string>>(new Set());

  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);

  // loadApps reads the DB-backed list synchronously (no agent hop).
  const loadApps = useCallback(async () => {
    if (!isAuthenticated || !activeServerId) {
      if (mounted.current) {
        setApps([]);
        setLoading(false);
        setError(null);
      }
      return;
    }
    if (mounted.current) {
      setLoading(true);
      setError(null);
    }
    try {
      const res = await fetch(
        `${base}/api/v1/app/get-apps?server_id=${encodeURIComponent(activeServerId)}`,
        { credentials: "include" },
      );
      if (!res.ok) {
        throw new Error(`Failed to load apps: ${res.status}`);
      }
      const result = (await res.json()) as GetAppsResponse;
      if (!mounted.current) return;
      setApps((result.data?.apps ?? []).map(toApp));
    } catch (e) {
      if (!mounted.current) return;
      setApps([]);
      setError(e instanceof Error ? e.message : "Failed to load apps");
    } finally {
      if (mounted.current) setLoading(false);
    }
  }, [isAuthenticated, activeServerId]);

  // refresh does the two-stage load: instant DB read, then dispatch an agent
  // reconcile (apps.list). The reconcile result returns over SSE; when it
  // arrives we re-read the now-synced DB list (handled by the subscription).
  const refresh = useCallback(async () => {
    await loadApps();
    if (!isAuthenticated || !activeServerId) return;
    try {
      const res = await fetch(
        `${base}/api/v1/app/refresh-apps?server_id=${encodeURIComponent(activeServerId)}`,
        { method: "POST", credentials: "include" },
      );
      if (!res.ok) return;
      const accepted = (await res.json()) as AcceptedResponse;
      const ref = accepted.data?.request_id;
      if (ref) pendingRefs.current.add(ref);
    } catch {
      // Best-effort reconcile; the DB read above already populated the UI.
    }
  }, [loadApps, isAuthenticated, activeServerId]);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  // Re-read the apps list when a dispatched command's result arrives over SSE.
  // The backend persists/reconciles the DB before publishing, so a plain
  // re-read picks up the synced state.
  useEffect(() => {
    const unsubscribe = subscribe((n) => {
      if (n.type !== "operation_result") return;
      if (!n.ref || !pendingRefs.current.has(n.ref)) return;
      pendingRefs.current.delete(n.ref);
      void loadApps();
    });
    return unsubscribe;
  }, [subscribe, loadApps]);

  const value = useMemo(
    () => ({ apps, loading, error, refresh }),
    [apps, loading, error, refresh],
  );

  return <AppsContext.Provider value={value}>{children}</AppsContext.Provider>;
}
