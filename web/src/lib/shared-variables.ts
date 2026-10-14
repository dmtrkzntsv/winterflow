// Org- and server-level shared variables: types mirroring the API, the
// browser fan-out encryption (one ECIES ciphertext per server key, so the
// API never sees a secret), small display helpers and the API client.
import { apiBase } from "@/config";
import { createJsonClient, jsonPost } from "@/lib/api";
import { encryptSecret } from "@/lib/ecies";

export type VariableScope = "org" | "server";

export type SharedVariable = {
  id: string;
  name: string;
  scope: VariableScope;
  is_secret: boolean;
  // "<encrypted>" for secrets: no route ever returns a secret value.
  value: string;
  description: string;
  updated_at: string;
  updated_by: string;
  // Keyed servers in scope without a copy of this secret.
  missing_on: string[];
};

export type InheritedVariable = SharedVariable & { overridden: boolean };

export type BuiltinVariable = { name: string; value: string; description: string };

export type ServerSync = {
  server_id: string;
  name: string;
  has_key: boolean;
  revision: number;
  agent_revision: number | null;
  state: "synced" | "pending";
};

// The browser adds "failed" from vars.sync_failed events.
export type SyncState = "synced" | "pending" | "failed";

export type OrgVariables = { variables: SharedVariable[]; servers: ServerSync[] };

export type ServerVariables = {
  variables: SharedVariable[];
  inherited: InheritedVariable[];
  builtins: BuiltinVariable[];
  missing_org_secrets: string[];
  sync: ServerSync;
};

export type EncryptionKey = { server_id: string; name: string; public_key: string };
export type PendingServer = { server_id: string; name: string };
export type EncryptionKeys = { servers: EncryptionKey[]; pending_servers: PendingServer[] };

type SaveVariableBase = {
  name: string;
  description: string;
  reload?: boolean;
};

// Ruling P9a: a secret save never carries a plaintext value (the API 400s on
// "<encrypted>"); a plain save must carry one.
export type SaveVariable =
  | (SaveVariableBase & { is_secret: true; value?: never; ciphertexts?: Record<string, string> })
  | (SaveVariableBase & { is_secret: false; value: string; ciphertexts?: never });

export type ResyncTarget = { server_id?: string; variable_id?: string };
export type ReloadTarget = { server_id?: string; variable_id?: string; names?: string[]; app_ids?: string[] };

// VarsReloaded is the payload of a vars.reloaded SSE event.
export type VarsReloaded = { server_id: string; app_id: string; ok: boolean; error?: string; names: string[] };

// SharedName is a variable an app can reference, by scope, never its value.
export type SharedName = { name: string; scope: VariableScope | "builtin" };

export const ENCRYPTED = "<encrypted>";

const NAME = /^[A-Za-z_][A-Za-z0-9_]*$/;

// variableNameError mirrors model.ValidateSharedVariableName.
export function variableNameError(name: string): string {
  if (!name) return "Name is required";
  if (name.length > 255) return "Name is longer than 255 characters";
  if (!NAME.test(name)) return "Use letters, digits and _, not starting with a digit";
  if (name === "COMPOSE_PROJECT_NAME" || name.startsWith("WF_")) return `${name} is reserved`;
  return "";
}

// encryptForServers encrypts value once per server key (all of keys, or
// only the server ids in only — a gap fill). Each result is readable only
// by that server's agent.
export async function encryptForServers(
  value: string,
  keys: EncryptionKey[],
  only?: string[],
): Promise<Record<string, string>> {
  const out: Record<string, string> = {};
  for (const k of keys) {
    if (only && !only.includes(k.server_id)) continue;
    out[k.server_id] = await encryptSecret(value, k.public_key);
  }
  return out;
}

export function serverNameMap(keys: EncryptionKeys): Map<string, string> {
  const m = new Map<string, string>();
  for (const s of keys.servers) m.set(s.server_id, s.name);
  for (const s of keys.pending_servers) m.set(s.server_id, s.name);
  return m;
}

// coverageLabel says which servers hold a copy of a secret. missing_on
// counts keyed servers only, so "All servers" is claimed only when every
// server in the org has a key: pending servers (no key yet) and an org with
// no keyed server at all are spelled out instead.
export function coverageLabel(v: SharedVariable, names: Map<string, string>, keys?: EncryptionKeys): string {
  if (!v.is_secret) return "";
  const pending = keys?.pending_servers.length ?? 0;
  const pendingNote = pending > 0 ? `${pending} pending (no key yet)` : "";
  if (v.missing_on.length > 0) {
    const missing = `Missing on ${v.missing_on.map((id) => names.get(id) ?? id).join(", ")}`;
    return pendingNote ? `${missing}; ${pendingNote}` : missing;
  }
  if (keys && keys.servers.length === 0) return "No server has a key yet";
  if (pendingNote) return `All keyed servers; ${pendingNote}`;
  return "All servers";
}

// coverageIncomplete is true when coverageLabel names a gap.
export function coverageIncomplete(v: SharedVariable, keys?: EncryptionKeys): boolean {
  if (!v.is_secret) return false;
  if (v.missing_on.length > 0) return true;
  return !!keys && (keys.servers.length === 0 || keys.pending_servers.length > 0);
}

export function pushSummary(count: number, pending: number): string {
  const s = `Will be pushed to ${count} ${count === 1 ? "server" : "servers"}`;
  return pending > 0 ? `${s} (${pending} pending, no key yet)` : s;
}

// applySyncEvent overlays a vars.synced / vars.sync_failed event on
// per-server sync states; other events return states unchanged.
export function applySyncEvent(
  states: Record<string, SyncState>,
  n: { type: string; payload?: unknown },
): Record<string, SyncState> {
  const id = (n.payload as { server_id?: string } | undefined)?.server_id;
  if (!id) return states;
  if (n.type === "vars.synced") return { ...states, [id]: "synced" };
  if (n.type === "vars.sync_failed") return { ...states, [id]: "failed" };
  return states;
}

// sharedNames lists what an app on the server can reference: the server's
// variables, the org's it does not override, and the built-ins.
export function sharedNames(v: ServerVariables): SharedName[] {
  const out: SharedName[] = v.variables.map((x) => ({ name: x.name, scope: "server" as const }));
  for (const x of v.inherited) if (!x.overridden) out.push({ name: x.name, scope: "org" });
  for (const b of v.builtins) out.push({ name: b.name, scope: "builtin" });
  return out.sort((a, b) => (a.name < b.name ? -1 : a.name > b.name ? 1 : 0));
}

// overrideNote tells an app variable it shadows a shared one.
export function overrideNote(name: string, shared: SharedName[]): string {
  const hit = shared.find((s) => s.name === name);
  if (!hit || hit.scope === "builtin") return "";
  return `overrides ${hit.scope} value`;
}

export type SharedVariablesApi = {
  listOrg(): Promise<OrgVariables>;
  listServer(serverId: string): Promise<ServerVariables>;
  keys(): Promise<EncryptionKeys>;
  saveOrg(body: SaveVariable): Promise<SharedVariable>;
  saveServer(serverId: string, body: SaveVariable): Promise<SharedVariable>;
  deleteOrg(id: string, reload?: boolean): Promise<void>;
  deleteServer(serverId: string, id: string, reload?: boolean): Promise<void>;
  resync(target: ResyncTarget): Promise<string[]>;
  reload(target: ReloadTarget): Promise<string[]>;
};

export function createSharedVariablesApi(fetchImpl?: typeof fetch): SharedVariablesApi {
  const { call } = createJsonClient(apiBase, fetchImpl);
  const post = <T>(path: string, payload: unknown) => call<T>(path, jsonPost(payload));
  const dispatched = (d: { servers?: string[] | null } | null) => d?.servers ?? [];
  return {
    listOrg: () => call<OrgVariables>("/api/v1/org/list-shared-variables"),
    listServer: (serverId) =>
      call<ServerVariables>(`/api/v1/server/list-shared-variables?server_id=${encodeURIComponent(serverId)}`),
    keys: () => call<EncryptionKeys>("/api/v1/org/list-encryption-keys"),
    saveOrg: (body) => post<SharedVariable>("/api/v1/org/save-shared-variable", body),
    saveServer: (serverId, body) =>
      post<SharedVariable>("/api/v1/server/save-shared-variable", { ...body, server_id: serverId }),
    deleteOrg: async (id, reload) => {
      await post<null>("/api/v1/org/delete-shared-variable", reload ? { id, reload } : { id });
    },
    deleteServer: async (serverId, id, reload) => {
      await post<null>(
        "/api/v1/server/delete-shared-variable",
        reload ? { server_id: serverId, id, reload } : { server_id: serverId, id },
      );
    },
    resync: (target) => post<{ servers?: string[] | null }>("/api/v1/shared-variables/resync", target).then(dispatched),
    reload: (target) => post<{ servers?: string[] | null }>("/api/v1/shared-variables/reload", target).then(dispatched),
  };
}

// VariableRow is one row of Settings → Shared variables: an org variable
// (every server) or the active server's own, and what the list says about
// it.
export type VariableRow = {
  variable: SharedVariable;
  // "All servers" for the org's, "{server} only" for the active server's.
  scopeLabel: string;
  // An org variable the active server replaces with a value of its own.
  overriddenHere: boolean;
};

// variableRows merges the org's variables and the active server's own into
// one list sorted by name, the org's row first when both define a name.
export function variableRows(org: SharedVariable[], server: SharedVariable[], serverName: string): VariableRow[] {
  const own = new Set(server.map((v) => v.name));
  const rows: VariableRow[] = [
    ...org.map((v) => ({ variable: v, scopeLabel: "All servers", overriddenHere: own.has(v.name) })),
    ...server.map((v) => ({ variable: v, scopeLabel: `${serverName} only`, overriddenHere: false })),
  ];
  return rows.sort((a, b) => {
    if (a.variable.name !== b.variable.name) return a.variable.name < b.variable.name ? -1 : 1;
    return a.variable.scope === b.variable.scope ? 0 : a.variable.scope === "org" ? -1 : 1;
  });
}

// failedServers lists the servers whose last sync failed, sorted by name
// (an unknown server by its id).
export function failedServers(
  sync: Record<string, SyncState>,
  names: Map<string, string>,
): { id: string; name: string }[] {
  return Object.entries(sync)
    .filter(([, state]) => state === "failed")
    .map(([id]) => ({ id, name: names.get(id) ?? id }))
    .sort((a, b) => a.name.localeCompare(b.name));
}
