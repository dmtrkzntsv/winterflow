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

import {
  AppsContext,
  type App,
  type AppDetailPayload,
  type AppRevisions,
  type ControlAction,
} from "./apps-context-base";

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

type AppStatusResponse = {
  app_id: string;
  status_code: number;
};

type GetAppsStatusResponse = {
  data?: { apps?: AppStatusResponse[] | null };
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
  const { subscribe, waitFor, connected } = useNotifications();
  const [apps, setApps] = useState<App[]>([]);
  const [statusByApp, setStatusByApp] = useState<Record<string, number>>({});
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

  // loadStatus pulls the live container-status snapshot (in-memory, TTL'd).
  const loadStatus = useCallback(async () => {
    if (!isAuthenticated || !activeServerId) {
      if (mounted.current) setStatusByApp({});
      return;
    }
    try {
      const res = await fetch(
        `${base}/api/v1/app/get-apps-status?server_id=${encodeURIComponent(activeServerId)}`,
        { credentials: "include" },
      );
      if (!res.ok) return;
      const result = (await res.json()) as GetAppsStatusResponse;
      if (!mounted.current) return;
      const map: Record<string, number> = {};
      for (const s of result.data?.apps ?? []) {
        map[s.app_id] = s.status_code;
      }
      setStatusByApp(map);
    } catch {
      // Status is best-effort; absence renders as "unknown".
    }
  }, [isAuthenticated, activeServerId]);

  // Status is pushed over SSE (apps_status, every agent report); this fetch
  // only seeds the snapshot on load/server switch and re-seeds after the
  // stream (re)connects, covering events missed while disconnected. No
  // interval polling.
  useEffect(() => {
    void loadStatus();
  }, [loadStatus, connected]);

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

  // Live status push: the agent reports apps.status every 30s and the API fans
  // it out as an apps_status notification. The 15s poll above stays as a
  // reconnect/seed fallback.
  useEffect(() => {
    return subscribe((n) => {
      if (n.type !== "apps_status") return;
      const p = n.payload as
        | {
            server_id?: string;
            apps?: { app_id: string; status_code: number }[] | null;
          }
        | undefined;
      if (!p?.server_id || p.server_id !== activeServerId) return;
      const map: Record<string, number> = {};
      for (const s of p.apps ?? []) {
        map[s.app_id] = s.status_code;
      }
      setStatusByApp(map);
    });
  }, [subscribe, activeServerId]);

  // dispatchAndWait POSTs a fire-and-forward command, awaits its SSE result by
  // request_id, then refreshes the list and status.
  const dispatchAndWait = useCallback(
    async (path: string, body: Record<string, unknown>) => {
      if (!activeServerId) throw new Error("No active server");
      const res = await fetch(`${base}/api/v1/app/${path}`, {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ ...body, server_id: activeServerId }),
      });
      if (!res.ok) throw new Error(`Request failed: ${res.status}`);
      const accepted = (await res.json()) as AcceptedResponse;
      const ref = accepted.data?.request_id;
      if (ref) {
        const result = await waitFor(ref);
        if (result.status && result.status !== 0) {
          throw new Error(result.error || "Operation failed");
        }
      }
      await Promise.all([loadApps(), loadStatus()]);
    },
    [activeServerId, waitFor, loadApps, loadStatus],
  );

  const control = useCallback(
    (appId: string, action: ControlAction) =>
      dispatchAndWait("control-app", { app_id: appId, action }),
    [dispatchAndWait],
  );

  const remove = useCallback(
    (appId: string) => dispatchAndWait("delete-app", { app_id: appId }),
    [dispatchAndWait],
  );

  const rename = useCallback(
    (appId: string, name: string) =>
      dispatchAndWait("rename-app", { app_id: appId, name }),
    [dispatchAndWait],
  );

  // getPublicKey fetches the server's ECIES public key (for encrypting secrets).
  const getPublicKey = useCallback(async (): Promise<string> => {
    if (!activeServerId) throw new Error("No active server");
    const res = await fetch(
      `${base}/api/v1/server/get-public-key?server_id=${encodeURIComponent(activeServerId)}`,
      { credentials: "include" },
    );
    if (!res.ok) throw new Error(`Failed to fetch public key: ${res.status}`);
    const result = (await res.json()) as { data?: { public_key?: string } };
    const key = result.data?.public_key;
    if (!key) throw new Error("Server returned no public key");
    return key;
  }, [activeServerId]);

  // getApp fetches an app's config + files + variables from the agent (app.get,
  // result over SSE). Used to populate the editor for editing. Secret values
  // come back masked by the agent; the editor sends them back unchanged.
  const getApp = useCallback(
    async (appId: string): Promise<AppDetailPayload> => {
      if (!activeServerId) throw new Error("No active server");
      const res = await fetch(
        `${base}/api/v1/app/get-app?server_id=${encodeURIComponent(
          activeServerId,
        )}&app_id=${encodeURIComponent(appId)}`,
        { credentials: "include" },
      );
      if (!res.ok) throw new Error(`Failed to fetch app: ${res.status}`);
      const accepted = (await res.json()) as AcceptedResponse;
      const ref = accepted.data?.request_id;
      if (!ref) throw new Error("No request id");
      const result = await waitFor(ref);
      if (result.status && result.status !== 0) {
        throw new Error(result.error || "Failed to fetch app");
      }
      return result.payload as AppDetailPayload;
    },
    [activeServerId, waitFor],
  );

  // getRevisions fetches the app's git history (app.revisions over SSE).
  const getRevisions = useCallback(
    async (appId: string): Promise<AppRevisions> => {
      if (!activeServerId) throw new Error("No active server");
      const res = await fetch(
        `${base}/api/v1/app/get-revisions?server_id=${encodeURIComponent(
          activeServerId,
        )}&app_id=${encodeURIComponent(appId)}`,
        { credentials: "include" },
      );
      if (!res.ok) throw new Error(`Failed to fetch revisions: ${res.status}`);
      const accepted = (await res.json()) as AcceptedResponse;
      const ref = accepted.data?.request_id;
      if (!ref) throw new Error("No request id");
      const result = await waitFor(ref);
      if (result.status && result.status !== 0) {
        throw new Error(result.error || "Failed to fetch revisions");
      }
      const payload = result.payload as
        | { current?: string; revisions?: AppRevisions["revisions"] | null }
        | undefined;
      return {
        current: payload?.current ?? "",
        revisions: payload?.revisions ?? [],
      };
    },
    [activeServerId, waitFor],
  );

  // rollback restores a previous commit as a new revision and redeploys.
  const rollback = useCallback(
    (appId: string, hash: string) =>
      dispatchAndWait("rollback-app", { app_id: appId, hash }),
    [dispatchAndWait],
  );

  // createApp dispatches app.save with the full payload and awaits its result.
  const createApp = useCallback(
    async (body: {
      source?: unknown;
      app: Record<string, unknown>;
      config: unknown;
      files: { name: string; content: string; encrypted: boolean }[];
      variables: { name: string; content: string; encrypted: boolean }[];
    }) => {
      await dispatchAndWait("create-app", {
        app: body.app,
        config: body.config,
        files: body.files,
        variables: body.variables,
        ...(body.source ? { source: body.source } : {}),
      });
    },
    [dispatchAndWait],
  );

  const value = useMemo(
    () => ({
      apps,
      statusByApp,
      loading,
      error,
      refresh,
      control,
      remove,
      rename,
      createApp,
      getPublicKey,
      getApp,
      getRevisions,
      rollback,
    }),
    [
      apps,
      statusByApp,
      loading,
      error,
      refresh,
      control,
      remove,
      rename,
      createApp,
      getPublicKey,
      getApp,
      getRevisions,
      rollback,
    ],
  );

  return <AppsContext.Provider value={value}>{children}</AppsContext.Provider>;
}
