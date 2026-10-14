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
import { WaitTimeoutError } from "@/context/notifications-context-base";
import { apiBase as base } from "@/config";
import { dispatchCommand } from "@/lib/api";
import type { BackupHealth } from "@/lib/backups";
import { forgetAppDetail } from "@/hooks/use-app-detail";
import { settleLateCreate } from "@/lib/held-creates";
import { forgetRevisions } from "@/lib/revisions";
import { createSharedVariablesApi, type VarsReloaded } from "@/lib/shared-variables";
import { splitStatus, type AppStatusWire, type ContainerStatusWire } from "@/lib/app-status";

import {
  AppsContext,
  AppStatusCode,
  DELETE_APP_TIMEOUT_MS,
  SaveFailedError,
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
  template_id?: string;
};

type GetAppsResponse = {
  success: boolean;
  data?: { apps?: AppResponse[] | null };
};

// keepIfSame is a state update that keeps the previous value when the new
// one reads the same: every agent report re-sends the whole snapshot, and a
// new object would re-render every consumer for nothing.
const keepIfSame =
  <T,>(next: T) =>
  (prev: T): T =>
    JSON.stringify(prev) === JSON.stringify(next) ? prev : next;

type GetAppsStatusResponse = {
  data?: { apps?: AppStatusWire[] | null };
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
  templateId: a.template_id || undefined,
});

export function AppsProvider({ children }: { children: ReactNode }) {
  const { isAuthenticated } = useAuth();
  const { activeServerId } = useServers();
  const { subscribe, waitFor, connected } = useNotifications();
  const [apps, setApps] = useState<App[]>([]);
  const [statusByApp, setStatusByApp] = useState<Record<string, number>>({});
  const [staleVarsByApp, setStaleVarsByApp] = useState<Record<string, string[]>>({});
  const [backupsByApp, setBackupsByApp] = useState<Record<string, BackupHealth[]>>({});
  const [containersByApp, setContainersByApp] = useState<Record<string, ContainerStatusWire[]>>({});
  const [deployFailures, setDeployFailures] = useState<Record<string, string>>({});
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

  const noteDeployFailure = useCallback((appId: string, error: string) => {
    setDeployFailures((prev) => ({ ...prev, [appId]: error }));
  }, []);
  // clearDeployFailure drops an app's failed deploy: it runs now, or a
  // later deploy of it went through.
  const clearDeployFailure = useCallback((appId: string) => {
    setDeployFailures((prev) => {
      if (!(appId in prev)) return prev;
      const next = { ...prev };
      delete next[appId];
      return next;
    });
  }, []);
  // A status report showing a failed app running ends its failure.
  useEffect(() => {
    setDeployFailures((prev) => {
      const done = Object.keys(prev).filter((id) => statusByApp[id] === AppStatusCode.Active);
      if (done.length === 0) return prev;
      const next = { ...prev };
      for (const id of done) delete next[id];
      return next;
    });
  }, [statusByApp]);

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
        setStaleVarsByApp({});
        setBackupsByApp({});
        setContainersByApp({});
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
      const { status, stale, backups, containers } = splitStatus(result.data?.apps);
      setStatusByApp(keepIfSame(status));
      setStaleVarsByApp(keepIfSame(stale));
      setBackupsByApp(keepIfSame(backups));
      setContainersByApp(keepIfSame(containers));
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

  // reconcile is refresh that waits: the agent's list synced into the
  // catalog, then read again (with the status).
  const reconcile = useCallback(async (): Promise<App[]> => {
    if (!activeServerId) throw new Error("No active server");
    await dispatchCommand(
      waitFor,
      `${base}/api/v1/app/refresh-apps?server_id=${encodeURIComponent(activeServerId)}`,
      { method: "POST" },
      { refOptional: true },
    );
    const [list] = await Promise.all([loadApps(), loadStatus()]);
    return list;
  }, [activeServerId, waitFor, loadApps, loadStatus]);

  // Re-read the apps list when a dispatched command's result arrives over SSE.
  // The backend persists/reconciles the DB before publishing, so a plain
  // re-read picks up the synced state.
  useEffect(() => {
    const unsubscribe = subscribe((n) => {
      if (n.type !== "operation_result") return;
      if (!n.ref || !pendingRefs.current.has(n.ref)) return;
      pendingRefs.current.delete(n.ref);
      // A create that outlived its wait gives its name back (and its form,
      // when open, hears how it ended) before the list is read again.
      settleLateCreate(n.ref, { failed: !!n.status, error: n.error || "Operation failed" });
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
            apps?: AppStatusWire[] | null;
          }
        | undefined;
      if (!p?.server_id || p.server_id !== activeServerId) return;
      const { status, stale, backups, containers } = splitStatus(p.apps);
      setStatusByApp(keepIfSame(status));
      setStaleVarsByApp(keepIfSame(stale));
      setBackupsByApp(keepIfSame(backups));
      setContainersByApp(keepIfSame(containers));
    });
  }, [subscribe, activeServerId]);

  // dispatchAndWait POSTs a fire-and-forward command, awaits its SSE result
  // by request_id, refreshes the list and status, and returns the refreshed
  // list together with the result payload. A command that outlives the wait
  // still re-reads the list when its result arrives (a slow create appears).
  const dispatchAndWait = useCallback(
    async (
      path: string,
      body: Record<string, unknown>,
      opts: { timeoutMs?: number; failure?: new (message: string) => Error } = {},
    ): Promise<{ list: App[]; payload: unknown }> => {
      if (!activeServerId) throw new Error("No active server");
      let ref = "";
      // A refused command (400) carries the reason, e.g. an unknown storage.
      const payload = await dispatchCommand(
        waitFor,
        `${base}/api/v1/app/${path}`,
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ ...body, server_id: activeServerId }),
        },
        { timeoutMs: opts.timeoutMs, failure: opts.failure, refOptional: true, onAccepted: (r) => (ref = r) },
      ).catch((e: unknown) => {
        if (e instanceof WaitTimeoutError && ref) {
          pendingRefs.current.add(ref);
          e.ref = ref;
        }
        throw e;
      });
      const [list] = await Promise.all([loadApps(), loadStatus()]);
      return { list, payload };
    },
    [activeServerId, waitFor, loadApps, loadStatus],
  );

  const control = useCallback(
    async (appId: string, action: ControlAction) => {
      await dispatchAndWait("control-app", { app_id: appId, action });
      if (action !== "stop") clearDeployFailure(appId);
      // A start (and a pull, which redeploys) can bring an undeployed draft
      // live without a commit; a stop or restart moves nothing.
      if (action === "start" || action === "update") forgetRevisions(appId);
      // A pull can move a Git app's source (a new commit) while the version
      // it is listed under stays: the page's read of it is stale.
      if (action === "update") forgetAppDetail(appId);
    },
    [dispatchAndWait, clearDeployFailure],
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
      forgetRevisions(appId, { gone: true });
      clearDeployFailure(appId);
      const res = payload as
        | { warnings?: string[]; volumes_deleted?: boolean; backups_deleted?: boolean }
        | undefined;
      return {
        warnings: res?.warnings ?? [],
        volumesDeleted: res?.volumes_deleted ?? false,
        backupsDeleted: res?.backups_deleted ?? false,
      };
    },
    [dispatchAndWait, clearDeployFailure],
  );

  const rename = useCallback(
    async (appId: string, name: string) => {
      await dispatchAndWait("rename-app", { app_id: appId, name });
      // A rename is a commit (the name is in the app's config) the version
      // it is listed under may not show: a kept read would save the old
      // name back.
      forgetRevisions(appId);
      forgetAppDetail(appId);
    },
    [dispatchAndWait],
  );

  const reloadSharedVars = useCallback(
    async (appId: string) => {
      if (!activeServerId) throw new Error("No active server");
      await createSharedVariablesApi().reload({ server_id: activeServerId, app_ids: [appId] });
    },
    [activeServerId],
  );

  // A successful reload applied the new values: the app is fresh until the
  // next apps_status says otherwise.
  useEffect(() => {
    return subscribe((n) => {
      if (n.type !== "vars.reloaded") return;
      const p = n.payload as VarsReloaded | undefined;
      if (!p?.ok || p.server_id !== activeServerId) return;
      setStaleVarsByApp((prev) => {
        if (!(p.app_id in prev)) return prev;
        const next = { ...prev };
        delete next[p.app_id];
        return next;
      });
    });
  }, [subscribe, activeServerId]);

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
      // The old tree is a new commit, under a version that may not move: a
      // kept read would save the pre-rollback tree back.
      forgetRevisions(appId);
      forgetAppDetail(appId);
      clearDeployFailure(appId);
    },
    [dispatchAndWait, clearDeployFailure],
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
    }, opts: { timeoutMs?: number } = {}) => {
      const { list } = await dispatchAndWait(
        "save-app",
        {
          app: body.app,
          config: body.config,
          files: body.files,
          variables: body.variables,
          ...(body.source ? { source: body.source } : {}),
          ...(body.draft ? { draft: true } : {}),
        },
        { failure: SaveFailedError, timeoutMs: opts.timeoutMs },
      );
      if (typeof body.app.id === "string") {
        // The app changed, and the version it is listed under may not have
        // moved (the API stores none after a save): drop what was kept of it.
        forgetRevisions(body.app.id);
        forgetAppDetail(body.app.id);
        // A deploy went through: the app no longer reads Failed.
        if (!body.draft) clearDeployFailure(body.app.id);
      }
      return list;
    },
    [dispatchAndWait, clearDeployFailure],
  );

  const value = useMemo(
    () => ({
      apps,
      statusByApp,
      staleVarsByApp,
      reloadSharedVars,
      backupsByApp,
      containersByApp,
      deployFailures,
      noteDeployFailure,
      loading,
      error,
      refresh,
      reconcile,
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
      staleVarsByApp,
      reloadSharedVars,
      backupsByApp,
      containersByApp,
      deployFailures,
      noteDeployFailure,
      loading,
      error,
      refresh,
      reconcile,
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
