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
  type VariableScope,
  type VarsReloaded,
} from "@/lib/shared-variables";

const NO_KEYS: EncryptionKeys = { servers: [], pending_servers: [] };

const errorText = (e: unknown) => (e instanceof Error ? e.message : undefined);

// A "server" scope without a server must never fall back to the org's
// scope: the change would reach every server.
const NO_SERVER = "No server is selected, so this change can't be made to its own variables.";

type Page = { org: OrgVariables; server: ServerVariables | null; keys: EncryptionKeys };

// SyncInfo is each server's sync state, and since when a server has read
// "pending" (ms), so the page can wait before it says anything about it.
type SyncInfo = { states: Record<string, SyncState>; since: Record<string, number> };

const withSince = (prev: SyncInfo, states: Record<string, SyncState>, now: number): SyncInfo => ({
  states,
  since: Object.fromEntries(
    Object.entries(states)
      .filter(([, state]) => state === "pending")
      .map(([id]) => [id, prev.since[id] ?? now]),
  ),
});

// useSharedVariablesSettings holds Settings → Shared variables: the org's
// variables and the active server's own (serverId null: there is none),
// the encryption keys, every server's sync state kept live from
// vars.synced / vars.sync_failed, failed-reload toasts from vars.reloaded,
// the add/edit/provide dialog and delete state, and the actions. Each
// action calls the route the separate org and server pages called before
// they merged (see the plan's route mapping).
export function useSharedVariablesSettings(api: SharedVariablesApi, serverId: string | null) {
  const { subscribe } = useNotifications();
  // Everything scope-bound is stored with the server it belongs to and only
  // exposed while that is still the active one, so switching servers never
  // shows (or acts on) the previous server's rows.
  const scopeKey = serverId ?? "";
  const [loaded, setLoaded] = useState<(Page & { key: string }) | null>(null);
  // The scope whose last load failed: another scope is loading, not failed.
  const [failedKey, setFailedKey] = useState<string | null>(null);
  const loadFailed = failedKey === scopeKey;
  const [syncInfo, setSyncInfo] = useState<SyncInfo>({ states: {}, since: {} });
  const sync = syncInfo.states;
  // The latest revision each server confirmed over SSE: a listing made
  // before that (it can arrive after) must not turn it back to pending.
  const confirmed = useRef<Record<string, number>>({});
  const [dialogState, setDialogState] = useState<{ key: string; mode: DialogMode } | null>(null);
  const [dialogKey, setDialogKey] = useState(0);
  // The variable a confirmation is about stays after it closes (its text
  // doesn't go blank while it animates out); only deleteOpen says it shows.
  const [deleteState, setDeleteState] = useState<{ key: string; variable: SharedVariable } | null>(null);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const current = loaded?.key === scopeKey ? loaded : null;
  const dialog = dialogState?.key === scopeKey ? dialogState.mode : null;
  const toDelete = deleteState?.key === scopeKey ? deleteState.variable : null;
  const keys = current?.keys ?? NO_KEYS;

  // Only the latest request's answer may land.
  const token = useRef(0);

  const fetchPage = useCallback(async (): Promise<Page> => {
    const [org, server, keys] = await Promise.all([
      api.listOrg(),
      serverId ? api.listServer(serverId) : Promise.resolve(null),
      api.keys(),
    ]);
    return { org, server, keys };
  }, [api, serverId]);

  const apply = useCallback(
    (page: Page) => {
      setLoaded({ key: scopeKey, ...page });
      setFailedKey(null);
      const listed = [...page.org.servers];
      if (page.server && serverId) listed.push(page.server.sync);
      // A later listing of the same server (its own sync) wins over the org's.
      const states: Record<string, SyncState> = Object.fromEntries(
        listed.map((s) => [
          s.server_id,
          s.state === "pending" && (confirmed.current[s.server_id] ?? -1) >= s.revision ? "synced" : s.state,
        ]),
      );
      const now = Date.now();
      setSyncInfo((prev) => withSince(prev, states, now));
    },
    [scopeKey, serverId],
  );

  const failed = useCallback(
    (e: unknown) => {
      setFailedKey(scopeKey);
      toast.error("Couldn't load the shared variables", { description: errorText(e) });
    },
    [scopeKey],
  );

  const load = useCallback(async () => {
    const mine = ++token.current;
    try {
      const page = await fetchPage();
      if (mine === token.current) apply(page);
    } catch (e) {
      if (mine === token.current) failed(e);
    }
  }, [fetchPage, apply, failed]);

  useEffect(() => {
    let mounted = true;
    const mine = ++token.current;
    fetchPage().then(
      (page) => {
        if (mounted && mine === token.current) apply(page);
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
        if (n.type === "vars.synced") {
          const p = n.payload as { server_id?: string; revision?: number } | undefined;
          if (p?.server_id && typeof p.revision === "number") {
            confirmed.current[p.server_id] = Math.max(confirmed.current[p.server_id] ?? -1, p.revision);
          }
        }
        const now = Date.now();
        setSyncInfo((prev) => withSince(prev, applySyncEvent(prev.states, n), now));
        if (n.type !== "vars.reloaded") return;
        const r = n.payload as VarsReloaded | undefined;
        if (!r || r.ok) return;
        // One toast per burst (a fixed id replaces the previous one).
        toast.error("Couldn't reload an app", { id: "vars-reload-failed", description: r.error });
      }),
    [subscribe],
  );

  // Names of every server: keyed and pending ones, and any the org's sync
  // list knows (a server whose sync failed may have no key).
  const serverNames = useMemo(() => {
    const names = serverNameMap(keys);
    for (const s of current?.org.servers ?? []) if (!names.has(s.server_id)) names.set(s.server_id, s.name);
    return names;
  }, [keys, current]);

  const openDialog = (mode: DialogMode) => {
    setDialogKey((k) => k + 1);
    setDialogState({ key: scopeKey, mode });
  };
  const closeDialog = () => setDialogState(null);
  const setToDelete = (variable: SharedVariable | null) => {
    if (variable) setDeleteState({ key: scopeKey, variable });
    setDeleteOpen(variable !== null);
  };

  // save stores a variable in its scope: the active server's own, or the
  // org's (every server). The dialog reports a failure.
  const save = async (body: SaveVariable, scope: VariableScope) => {
    if (scope === "server") {
      if (!serverId) throw new Error(NO_SERVER);
      await api.saveServer(serverId, body);
    } else {
      await api.saveOrg(body);
    }
    toast.success("Variable saved");
    await load();
  };

  // remove deletes a variable from its scope; a failure is reported and
  // rethrown (the confirmation stays open to retry).
  const remove = async (v: SharedVariable) => {
    try {
      if (v.scope === "server") {
        if (!serverId) throw new Error(NO_SERVER);
        await api.deleteServer(serverId, v.id);
      } else {
        await api.deleteOrg(v.id);
      }
      toast.success("Variable deleted");
      await load();
    } catch (e) {
      toast.error("Couldn't delete the variable", { description: errorText(e) });
      throw e;
    }
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

  // sendAgain pushes the variables again to servers whose sync failed: one
  // server by id, several with an empty target (every server). They read
  // pending until their agents answer, and go back on an error.
  const sendAgain = (ids: string[]) => {
    const before = sync;
    const mark = (pick: (id: string) => SyncState) => {
      const now = Date.now();
      setSyncInfo((prev) =>
        withSince(prev, { ...prev.states, ...Object.fromEntries(ids.map((id) => [id, pick(id)])) }, now),
      );
    };
    act(
      "Sent again",
      () => {
        mark(() => "pending");
        return api.resync(ids.length === 1 ? { server_id: ids[0] } : {});
      },
      () => mark((id) => before[id] ?? "failed"),
    );
  };

  // resendVariable pushes one org variable to every server again.
  const resendVariable = (v: SharedVariable) => act("Sent again", () => api.resync({ variable_id: v.id }));

  // reloadStale reloads the active server's running apps that use old values.
  const reloadStale = () => {
    if (serverId) act("Reload queued", () => api.reload({ server_id: serverId }));
  };

  // reloadVariable reloads the apps using a variable: a server variable's
  // on its server, an org variable's everywhere.
  const reloadVariable = (v: SharedVariable) =>
    act("Reload queued", async () => {
      if (v.scope !== "server") return api.reload({ variable_id: v.id });
      if (!serverId) throw new Error(NO_SERVER);
      return api.reload({ server_id: serverId, names: [v.name] });
    });

  return {
    org: current?.org ?? null,
    server: current?.server ?? null,
    keys,
    serverNames,
    sync,
    pendingSince: syncInfo.since,
    loadFailed,
    dialog,
    dialogKey,
    openDialog,
    closeDialog,
    toDelete,
    deleteOpen,
    setToDelete,
    save,
    remove,
    sendAgain,
    resendVariable,
    reloadStale,
    reloadVariable,
    load,
  };
}
