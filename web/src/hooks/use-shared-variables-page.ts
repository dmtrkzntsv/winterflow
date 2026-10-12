import { useCallback, useEffect, useMemo, useState } from "react";
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
  const [org, setOrg] = useState<OrgVariables | null>(null);
  const [server, setServer] = useState<ServerVariables | null>(null);
  const [keys, setKeys] = useState<EncryptionKeys>(NO_KEYS);
  const [sync, setSync] = useState<Record<string, SyncState>>({});
  const [dialog, setDialog] = useState<DialogMode | null>(null);
  const [dialogKey, setDialogKey] = useState(0);
  const [toDelete, setToDelete] = useState<SharedVariable | null>(null);

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
      setKeys(r.keys);
      if (r.server && serverId) {
        const state = r.server.sync.state;
        setServer(r.server);
        setSync((s) => ({ ...s, [serverId]: state }));
      }
      if (r.org) {
        setOrg(r.org);
        setSync(Object.fromEntries(r.org.servers.map((s) => [s.server_id, s.state])));
      }
    },
    [serverId],
  );

  const failed = useCallback(
    (e: unknown) =>
      toast.error(serverId ? "Failed to load variables" : "Failed to load org variables", {
        description: errorText(e),
      }),
    [serverId],
  );

  // load re-reads after a change.
  const load = useCallback(async () => {
    try {
      apply(await fetchPage());
    } catch (e) {
      failed(e);
    }
  }, [fetchPage, apply, failed]);

  useEffect(() => {
    let live = true;
    fetchPage().then(
      (r) => {
        if (live) apply(r);
      },
      (e: unknown) => {
        if (live) failed(e);
      },
    );
    return () => {
      live = false;
    };
  }, [fetchPage, apply, failed]);

  useEffect(
    () =>
      subscribe((n) => {
        setSync((s) => applySyncEvent(s, n));
        if (n.type !== "vars.reloaded") return;
        const r = n.payload as VarsReloaded | undefined;
        if (!r || r.ok || (serverId && r.server_id !== serverId)) return;
        toast.error("Couldn't reload an app", { description: r.error });
      }),
    [subscribe, serverId],
  );

  const serverNames = useMemo(() => serverNameMap(keys), [keys]);

  const openDialog = (mode: DialogMode) => {
    setDialogKey((k) => k + 1);
    setDialog(mode);
  };
  const closeDialog = () => setDialog(null);

  // A provide dialog on an inherited (org) row fills the org secret's gap;
  // everything else saves in the page's own scope.
  const save = async (body: SaveVariable) => {
    const orgSecret = dialog !== null && dialog.kind !== "create" && dialog.variable.scope === "org";
    if (serverId && !orgSecret) await api.saveServer(serverId, body);
    else await api.saveOrg(body);
    toast.success("Variable saved");
    await load();
  };

  const act = (done: string, fn: () => Promise<unknown>) => {
    fn().then(
      () => toast.success(done),
      (e: unknown) => toast.error("Request failed", { description: errorText(e) }),
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
  const resync = (target: { variable_id?: string } = {}) =>
    act("Resync sent", () => {
      if (serverId) setSync((s) => ({ ...s, [serverId]: "pending" }));
      return api.resync(serverId ? { server_id: serverId } : target);
    });

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
