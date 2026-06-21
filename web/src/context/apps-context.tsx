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
  const [apps, setApps] = useState<App[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const mounted = useRef(false);

  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);

  const fetchApps = useCallback(async () => {
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
      // Reconcile against the agent (source of truth); the result returns over
      // SSE and updates the DB. Best-effort, fire-and-forget.
      void fetch(
        `${base}/api/v1/app/refresh-apps?server_id=${encodeURIComponent(activeServerId)}`,
        { method: "POST", credentials: "include" },
      ).catch(() => {});
    } catch (e) {
      if (!mounted.current) return;
      setApps([]);
      setError(e instanceof Error ? e.message : "Failed to load apps");
    } finally {
      if (mounted.current) setLoading(false);
    }
  }, [isAuthenticated, activeServerId]);

  useEffect(() => {
    void fetchApps();
  }, [fetchApps]);

  const value = useMemo(
    () => ({ apps, loading, error, refresh: fetchApps }),
    [apps, loading, error, fetchApps],
  );

  return <AppsContext.Provider value={value}>{children}</AppsContext.Provider>;
}
