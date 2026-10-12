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
import { apiBase as base } from "@/config";
import { dispatchCommand } from "@/lib/api";
import type { BackupHealth } from "@/lib/backups";

import {
  AppsContext,
  DELETE_APP_TIMEOUT_MS,
  type App,
  type AppDetailPayload,
  type AppRevisions,
  type DeleteAppResult,
  type ControlAction,
} from "./apps-context-base";

type AppDomainResponse = {
  domain: string;
  ssl: boolean;
  kind: "route" | "redirect";
};

type AppResponse = {
  id: string;
  server_id: string;
  name: string;
  version: string;
  icon: string;
  color: string;
  created_at: string;
  domains?: AppDomainResponse[] | null;
};

type GetAppsResponse = {
  success: boolean;
  data?: { apps?: AppResponse[] | null };
};

type AppStatusResponse = {
  app_id: string;
  status_code: number;
  backups?: BackupHealth[] | null;
};

// byApp splits a status list into the status codes and the backup health
// by app id.
function byApp(list: AppStatusResponse[]) {
  const status: Record<string, number> = {};
  const backups: Record<string, BackupHealth[]> = {};
  for (const s of list) {
    status[s.app_id] = s.status_code;
    if (s.backups?.length) backups[s.app_id] = s.backups;
  }
  return { status, backups };
}

// keepIfSame is a state update that keeps the previous value when the new
// one reads the same: every agent report re-sends the whole snapshot, and a
// new object would re-render every consumer for nothing.
const keepIfSame =
  <T,>(next: T) =>
  (prev: T): T =>
    JSON.stringify(prev) === JSON.stringify(next) ? prev : next;

type GetAppsStatusResponse = {
  data?: { apps?: AppStatusResponse[] | null };
};

type AcceptedResponse = {
  data?: { request_id?: string };
};


const toApp = (a: AppResponse): App => ({
  id: a.id,
  serverId: a.server_id,
  name: a.name,
  version: a.version,
  icon: a.icon,
  color: a.color,
  createdAt: a.created_at,
  domains: a.domains ?? undefined,
});

export function AppsProvider({ children }: { children: ReactNode }) {
  const { isAuthenticated } = useAuth();
  const { activeServerId } = useServers();
  const { subscribe, waitFor, connected } = useNotifications();
  const [apps, setApps] = useState<App[]>([]);
  const [statusByApp, setStatusByApp] = useState<Record<string, number>>({});
  const [backupsByApp, setBackupsByApp] = useState<Record<string, BackupHealth[]>>({});
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
  const loadApps = useCallback(async (): Promise<App[]> => {
    if (!isAuthenticated || !activeServerId) {
      if (mounted.current) {
        setApps([]);
        setLoading(false);
        setError(null);
      }
      return [];
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
      if (!mounted.current) return [];
      const list = (result.data?.apps ?? []).map(toApp);
      setApps(list);
      return list;
    } catch (e) {
      if (!mounted.current) return [];
      setApps([]);
      setError(e instanceof Error ? e.message : "Failed to load apps");
      return [];
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
      if (mounted.current) {
        setStatusByApp({});
        setBackupsByApp({});
      }
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
      const { status, backups } = byApp(result.data?.apps ?? []);
      setStatusByApp(keepIfSame(status));
      setBackupsByApp(keepIfSame(backups));
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
            apps?: AppStatusResponse[] | null;
          }
        | undefined;
      if (!p?.server_id || p.server_id !== activeServerId) return;
      const { status, backups } = byApp(p.apps ?? []);
      setStatusByApp(keepIfSame(status));
      setBackupsByApp(keepIfSame(backups));
    });
  }, [subscribe, activeServerId]);

  // dispatchAndWait POSTs a fire-and-forward command, awaits its SSE result
  // by request_id, refreshes the list and status, and returns the refreshed
  // list together with the result payload.
  const dispatchAndWait = useCallback(
    async (
      path: string,
      body: Record<string, unknown>,
      opts: { timeoutMs?: number } = {},
    ): Promise<{ list: App[]; payload: unknown }> => {
      if (!activeServerId) throw new Error("No active server");
      // A refused command (400) carries the reason, e.g. an unknown storage.
      const payload = await dispatchCommand(
        waitFor,
        `${base}/api/v1/app/${path}`,
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ ...body, server_id: activeServerId }),
        },
        { timeoutMs: opts.timeoutMs, refOptional: true },
      );
      const [list] = await Promise.all([loadApps(), loadStatus()]);
      return { list, payload };
    },
    [activeServerId, waitFor, loadApps, loadStatus],
  );

  const control = useCallback(
    async (appId: string, action: ControlAction) => {
      await dispatchAndWait("control-app", { app_id: appId, action });
    },
    [dispatchAndWait],
  );

  const remove = useCallback(
    async (
      appId: string,
      opts?: { deleteVolumes?: boolean; deleteBackups?: boolean },
    ): Promise<DeleteAppResult> => {
      // The agent can take minutes to delete an app (it waits for a running
      // backup or restore, settles the backups in every storage): wait
      // longer than that, so its answer, warnings included, is what the user
      // sees.
      const { payload } = await dispatchAndWait(
        "delete-app",
        {
          app_id: appId,
          delete_volumes: opts?.deleteVolumes ?? false,
          delete_backups: opts?.deleteBackups ?? false,
        },
        { timeoutMs: DELETE_APP_TIMEOUT_MS },
      );
      const res = payload as
        | { warnings?: string[]; volumes_deleted?: boolean; backups_deleted?: boolean }
        | undefined;
      return {
        warnings: res?.warnings ?? [],
        volumesDeleted: res?.volumes_deleted ?? false,
        backupsDeleted: res?.backups_deleted ?? false,
      };
    },
    [dispatchAndWait],
  );

  const rename = useCallback(
    async (appId: string, name: string) => {
      await dispatchAndWait("rename-app", { app_id: appId, name });
    },
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

  // getImageTags lists registry tags for an image (image.tags over SSE).
  const getImageTags = useCallback(
    async (image: string): Promise<string[]> => {
      if (!activeServerId) throw new Error("No active server");
      const res = await fetch(
        `${base}/api/v1/image/get-tags?server_id=${encodeURIComponent(
          activeServerId,
        )}&image=${encodeURIComponent(image)}`,
        { credentials: "include" },
      );
      if (!res.ok) throw new Error(`Failed to fetch tags: ${res.status}`);
      const accepted = (await res.json()) as AcceptedResponse;
      const ref = accepted.data?.request_id;
      if (!ref) throw new Error("No request id");
      const result = await waitFor(ref);
      if (result.status && result.status !== 0) {
        throw new Error(result.error || "Failed to fetch tags");
      }
      const payload = result.payload as { tags?: string[] | null } | undefined;
      return payload?.tags ?? [];
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
        | {
            current?: string;
            deployed?: string;
            revisions?: AppRevisions["revisions"] | null;
          }
        | undefined;
      return {
        current: payload?.current ?? "",
        deployed: payload?.deployed ?? "",
        revisions: payload?.revisions ?? [],
      };
    },
    [activeServerId, waitFor],
  );

  // rollback restores a previous commit as a new revision and redeploys.
  const rollback = useCallback(
    async (appId: string, hash: string) => {
      await dispatchAndWait("rollback-app", { app_id: appId, hash });
    },
    [dispatchAndWait],
  );

  // saveApp dispatches app.save with the full payload (an upsert: app.id
  // present = edit, absent = create) and awaits its result.
  const saveApp = useCallback(
    async (body: {
      draft?: boolean;
      source?: unknown;
      app: Record<string, unknown>;
      config: unknown;
      files: { name: string; content: string; encrypted: boolean }[];
      variables: { name: string; content: string; encrypted: boolean }[];
    }) => {
      const { list } = await dispatchAndWait("save-app", {
        app: body.app,
        config: body.config,
        files: body.files,
        variables: body.variables,
        ...(body.source ? { source: body.source } : {}),
        ...(body.draft ? { draft: true } : {}),
      });
      return list;
    },
    [dispatchAndWait],
  );

  const value = useMemo(
    () => ({
      apps,
      statusByApp,
      backupsByApp,
      loading,
      error,
      refresh,
      control,
      remove,
      rename,
      saveApp,
      getPublicKey,
      getApp,
      getImageTags,
      getRevisions,
      rollback,
    }),
    [
      apps,
      statusByApp,
      backupsByApp,
      loading,
      error,
      refresh,
      control,
      remove,
      rename,
      saveApp,
      getPublicKey,
      getApp,
      getImageTags,
      getRevisions,
      rollback,
    ],
  );

  return <AppsContext.Provider value={value}>{children}</AppsContext.Provider>;
}
