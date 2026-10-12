import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { toast } from "sonner";

import type { DialogMode } from "@/components/shared-variable-dialog";
import { useNotifications } from "@/context/use-notifications";
import {
  applySyncEvent,
  serverNameMap,
  type EncryptionKeys,
  type OrgVariables,
  type SaveVariable,
  type ServerVariables,
  type SharedVariable,
  type SharedVariablesApi,
  type SyncState,
  type VarsReloaded,
} from "@/lib/shared-variables";

const NO_KEYS: EncryptionKeys = { servers: [], pending_servers: [] };

// Scope is what a shared-variables page manages: one server's variables
// (with the org's inherited) or the whole org's.
export type Scope = { kind: "org" } | { kind: "server"; serverId: string };

function errorText(e: unknown): string | undefined {
  return e instanceof Error ? e.message : undefined;
}

// useSharedVariablesPage holds the logic the server Variables tab and the
// org Variables page share: loading (+ encryption keys), per-server sync
// state kept live from vars.synced / vars.sync_failed, failed-reload toasts
// from vars.reloaded, the add/edit/provide dialog state, and the
// delete/resync/reload actions with their toasts. The page keeps only its
// layout. Pass a stable `scope` (memoize it); `org` is set for the org
// scope, `server` for the server scope.
export function useSharedVariablesPage(api: SharedVariablesApi, scope: Scope) {
  const { subscribe } = useNotifications();
  const serverId = scope.kind === "server" ? scope.serverId : null;
  const scopeKey = serverId ?? "org";
  // Everything scope-bound is stored with the scope it belongs to and only
  // exposed while that is still the current scope, so switching servers
  // never shows (or acts on) the previous server's rows.
  const [loaded, setLoaded] = useState<{
    key: string;
    org?: OrgVariables;
    server?: ServerVariables;
    keys: EncryptionKeys;
  } | null>(null);
  const [sync, setSync] = useState<Record<string, SyncState>>({});
  const [dialogState, setDialogState] = useState<{ key: string; mode: DialogMode } | null>(null);
  const [dialogKey, setDialogKey] = useState(0);
  const [deleteState, setDeleteState] = useState<{ key: string; variable: SharedVariable } | null>(null);
  const current = loaded?.key === scopeKey ? loaded : null;
  const org = current?.org ?? null;
  const server = current?.server ?? null;
  const keys = current?.keys ?? NO_KEYS;
  const dialog = dialogState?.key === scopeKey ? dialogState.mode : null;
  const toDelete = deleteState?.key === scopeKey ? deleteState.variable : null;
  const setToDelete = (variable: SharedVariable | null) =>
    setDeleteState(variable ? { key: scopeKey, variable } : null);

  // Only the latest request's response may land.
  const token = useRef(0);

  // fetchPage reads the scope's variables and the encryption keys; apply
  // puts them in state (a mount-time load must not set state synchronously).
  const fetchPage = useCallback(async () => {
    if (serverId) {
      const [server, keys] = await Promise.all([api.listServer(serverId), api.keys()]);
      return { server, keys };
    }
    const [org, keys] = await Promise.all([api.listOrg(), api.keys()]);
    return { org, keys };
  }, [api, serverId]);

  const apply = useCallback(
    (r: { server?: ServerVariables; org?: OrgVariables; keys: EncryptionKeys }) => {
      setLoaded({ key: scopeKey, ...r });
      if (r.server && serverId) {
        const state = r.server.sync.state;
        setSync((s) => ({ ...s, [serverId]: state }));
      }
      if (r.org) setSync(Object.fromEntries(r.org.servers.map((s) => [s.server_id, s.state])));
    },
    [scopeKey, serverId],
  );

  const failed = useCallback(
    (e: unknown) =>
      toast.error(serverId ? "Failed to load variables" : "Failed to load org variables", {
        description: errorText(e),
      }),
    [serverId],
  );

  // load (after a change) and the mount/scope-change effect each take a
  // token; only the latest request's response may land.
  const load = useCallback(async () => {
    const mine = ++token.current;
    try {
      const r = await fetchPage();
      if (mine === token.current) apply(r);
    } catch (e) {
      if (mine === token.current) failed(e);
    }
  }, [fetchPage, apply, failed]);

  useEffect(() => {
    let mounted = true;
    const mine = ++token.current;
    fetchPage().then(
      (r) => {
        if (mounted && mine === token.current) apply(r);
      },
      (e: unknown) => {
        if (mounted && mine === token.current) failed(e);
      },
    );
    return () => {
      mounted = false;
    };
  }, [fetchPage, apply, failed]);

  useEffect(
    () =>
      subscribe((n) => {
        setSync((s) => applySyncEvent(s, n));
        if (n.type !== "vars.reloaded") return;
        const r = n.payload as VarsReloaded | undefined;
        if (!r || r.ok || (serverId && r.server_id !== serverId)) return;
        // One toast per burst (a fixed id replaces the previous one).
        toast.error("Couldn't reload an app", { id: "vars-reload-failed", description: r.error });
      }),
    [subscribe, serverId],
  );

  const serverNames = useMemo(() => serverNameMap(keys), [keys]);

  const openDialog = (mode: DialogMode) => {
    setDialogKey((k) => k + 1);
    setDialogState({ key: scopeKey, mode });
  };
  const closeDialog = () => setDialogState(null);

  // A provide dialog on an inherited (org) row fills the org secret's gap;
  // everything else saves in the page's own scope.
  const save = async (body: SaveVariable) => {
    const orgSecret = dialog !== null && dialog.kind !== "create" && dialog.variable.scope === "org";
    if (serverId && !orgSecret) await api.saveServer(serverId, body);
    else await api.saveOrg(body);
    toast.success("Variable saved");
    await load();
  };

  const act = (done: string, fn: () => Promise<unknown>, onError?: () => void) => {
    fn().then(
      () => toast.success(done),
      (e: unknown) => {
        onError?.();
        toast.error("Request failed", { description: errorText(e) });
      },
    );
  };

  const remove = async (v: SharedVariable) => {
    try {
      if (serverId) await api.deleteServer(serverId, v.id);
      else await api.deleteOrg(v.id);
      toast.success("Variable deleted");
      await load();
    } catch (e) {
      toast.error("Couldn't delete the variable", { description: errorText(e) });
    }
  };

  // Marks the server (or every server) pending right away, then asks the API.
  const resync = (target: { variable_id?: string } = {}) => {
    const prev = serverId ? sync[serverId] : undefined;
    act(
      "Resync sent",
      () => {
        if (serverId) setSync((s) => ({ ...s, [serverId]: "pending" }));
        return api.resync(serverId ? { server_id: serverId } : target);
      },
      () => {
        if (serverId && prev) setSync((s) => ({ ...s, [serverId]: prev }));
      },
    );
  };

  const reload = (target: { names?: string[]; variable_id?: string } = {}) =>
    act("Reload queued", () => api.reload(serverId ? { server_id: serverId, ...target } : target));

  return {
    org,
    server,
    keys,
    serverNames,
    sync,
    dialog,
    dialogKey,
    openDialog,
    closeDialog,
    toDelete,
    setToDelete,
    save,
    remove,
    resync,
    reload,
    load,
  };
}
