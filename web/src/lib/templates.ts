// Templates and their instances: types mirroring the API
// (internal/domain/template, internal/domain/usecase/templates) and the API
// client. A template is org-level; an instance is an app on one server
// rendered from a template revision. Secret parameter values travel only as
// ECIES ciphertexts for the instance's server (lib/ecies); no route ever
// returns one.
import { apiBase } from "@/config";
import { WAIT_TIMEOUT } from "@/context/notifications-context-base";
import { readApiError } from "@/lib/api";
import type { Keep } from "@/lib/backups";
import { encryptSecret } from "@/lib/ecies";

export type Parameter = {
  name: string;
  secret?: boolean;
  required?: boolean;
  // null/absent: no default ("" is a default, but renders the same).
  default?: string | null;
  pattern?: string; // Go RE2
  description?: string;
};

// IngressRow routes host to the host port the compose file publishes; port
// is a number or exactly "${PARAM}".
export type IngressRow = { host: string; service?: string; port: number | string };

export type BackupDefault = { enabled: boolean; storage?: string; every?: string; keep?: Keep | null };

// TemplateBody is a revision's body. Go encodes an empty map or list as
// null.
export type TemplateBody = {
  files: Record<string, string> | null;
  parameters: Parameter[] | null;
  app_name: string;
  icon: string;
  color: string;
  ingress: IngressRow[] | null;
  backups: Record<string, BackupDefault> | null;
};

export type TemplateSummary = {
  id: string;
  name: string;
  latest_revision: number;
  instances: number; // pending and created
  behind: number; // on a revision older than the latest
  updated_at: string;
};

// instancesLabel is a template's "12 instances · 3 behind".
export function instancesLabel(t: Pick<TemplateSummary, "instances" | "behind">): string {
  const n = `${t.instances} ${t.instances === 1 ? "instance" : "instances"}`;
  return t.behind > 0 ? `${n} · ${t.behind} behind` : n;
}

export type TemplateRevisionInfo = { revision: number; note: string; created_at: string; created_by: string };

export type TemplateView = {
  id: string;
  name: string;
  latest_revision: number;
  revision: number;
  body: TemplateBody;
  note: string;
  created_at: string;
  created_by: string;
  revisions: TemplateRevisionInfo[] | null; // newest first
};

export type SavedTemplate = { id: string; name: string; revision: number; warnings: string[] | null };

export type SaveTemplateBody = {
  id?: string;
  name: string;
  body: TemplateBody;
  note?: string;
  base_revision: number; // the latest revision the editor read; 0 for a new template
};

export type InstanceState = "pending" | "created" | "deleted" | "unlinked";

export type InstanceView = {
  key: string;
  server_id: string;
  app_id: string;
  template_id: string;
  template_name: string;
  revision: number;
  parameters: Record<string, string> | null; // values callers set
  secrets: Record<string, string> | null; // name -> "<encrypted>"
  state: InstanceState;
  status_code: number;
  last_error: string;
  created_at: string;
  updated_at: string;
};

// InstanceValues are what create, update and upgrade send: plain values,
// secret ciphertexts and (while pending) backup storages, by name.
export type InstanceValues = {
  parameters?: Record<string, string>;
  secrets?: Record<string, string>;
  backups?: Record<string, string>;
};

export type CreateInstanceBody = InstanceValues & {
  server_id: string;
  key: string;
  template_id: string;
  revision?: number;
};

export type UpdateInstanceBody = InstanceValues & { server_id: string; key: string };

export type UpgradeInstanceBody = UpdateInstanceBody & { revision: number };

// INSTANCE_WAIT_MS bounds the wait for an instance command's result over
// SSE: a deploy may pull an image for minutes (the API's own wait bound).
export const INSTANCE_WAIT_MS = 10 * 60_000;

// ApiError is a refused request with its HTTP status (409: a concurrent
// edit, a key in use, a template with live instances).
export class ApiError extends Error {
  status: number;
  constructor(message: string, status: number) {
    super(message);
    this.status = status;
  }
}

async function refusal(res: Response): Promise<ApiError> {
  return new ApiError((await readApiError(res)).message, res.status);
}

// MANAGED_MARKER starts the API's 409 to save-app and rollback-app on an
// app a template instance manages (model.ManagedByTemplate).
const MANAGED_MARKER = "this app is managed by ";

export function isManagedByTemplate(message: string): boolean {
  return message.includes(MANAGED_MARKER);
}

export type WaitFor = (ref: string, timeoutMs?: number) => Promise<{ status?: number; error?: string }>;

export type TemplatesApi = {
  listTemplates(): Promise<TemplateSummary[]>;
  getTemplate(id: string, revision?: number): Promise<TemplateView>;
  saveTemplate(body: SaveTemplateBody): Promise<SavedTemplate>;
  deleteTemplate(id: string): Promise<void>;
  listInstances(filter: { serverId?: string; templateId?: string }): Promise<InstanceView[]>;
  getInstance(serverId: string, key: string): Promise<InstanceView>;
  // The instance commands resolve once the agent's result arrived (or at
  // once when there was nothing to run) and throw its error.
  createInstance(body: CreateInstanceBody): Promise<void>;
  updateInstance(body: UpdateInstanceBody): Promise<void>;
  upgradeInstance(body: UpgradeInstanceBody): Promise<void>;
  unlinkInstance(serverId: string, key: string): Promise<void>;
  // deleteInstance deletes the instance's app (what a failed deploy left of
  // it, for a pending one) and retires its key.
  deleteInstance(serverId: string, key: string): Promise<void>;
  // publicKey is the server's ECIES public key, read when a secret is about
  // to be encrypted (never cached: a re-keyed server must not get
  // ciphertexts for its old key).
  publicKey(serverId: string): Promise<string>;
};

export function createTemplatesApi(waitFor: WaitFor, fetchImpl?: typeof fetch): TemplatesApi {
  const doFetch: typeof fetch = fetchImpl ?? ((input, init) => fetch(input, init));
  async function call<T>(path: string, init?: RequestInit): Promise<T> {
    const res = await doFetch(`${apiBase}${path}`, { credentials: "include", ...init });
    if (!res.ok) throw await refusal(res);
    return ((await res.json()) as { data: T }).data;
  }
  const post = (payload: unknown): RequestInit => ({
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(payload),
  });
  // command posts an instance call without wait: a 202 names the request
  // whose result arrives over SSE; a 200 had nothing to run (a replayed
  // create of a created instance). The SSE replay buffer covers only
  // seconds, so a stream that dropped during a long deploy loses the
  // result: when the wait times out, the instance itself says how the
  // command ended (done: the state its success leaves).
  async function command(
    path: string,
    payload: { server_id: string; key: string },
    done: InstanceState = "created",
  ): Promise<void> {
    const sentAt = Date.now();
    const res = await doFetch(`${apiBase}${path}`, { credentials: "include", ...post(payload) });
    if (!res.ok) throw await refusal(res);
    if (res.status !== 202) return;
    const ref = ((await res.json()) as { data?: { request_id?: string } }).data?.request_id;
    if (!ref) throw new Error("No request id");
    let result: Awaited<ReturnType<WaitFor>>;
    try {
      result = await waitFor(ref, INSTANCE_WAIT_MS);
    } catch (e) {
      if (!(e instanceof Error) || e.message !== WAIT_TIMEOUT) throw e;
      return settled(payload, sentAt, done, e);
    }
    if (result.status) throw new Error(result.error || "The agent reported a failure");
  }
  // settled reads an instance whose command's result never arrived: an
  // error or the expected state recorded since sentAt ends the command,
  // anything else (still running, an older error, a failed read) is the
  // timeout. The comparison is between the browser's and the API's clocks.
  async function settled(
    { server_id, key }: { server_id: string; key: string },
    sentAt: number,
    done: InstanceState,
    timeout: Error,
  ): Promise<void> {
    const inst = await getInstance(server_id, key).catch(() => null);
    if (!inst || !(Date.parse(inst.updated_at) > sentAt)) throw timeout;
    if (inst.last_error) throw new Error(inst.last_error);
    if (inst.state !== done) throw timeout;
  }
  const qs = (params: Record<string, string | undefined>) => {
    const q = new URLSearchParams();
    for (const [k, v] of Object.entries(params)) if (v) q.set(k, v);
    const s = q.toString();
    return s ? `?${s}` : "";
  };
  const getInstance = (serverId: string, key: string) =>
    call<InstanceView>(`/api/v1/instance/get-instance${qs({ server_id: serverId, key })}`);
  return {
    listTemplates: () =>
      call<{ templates: TemplateSummary[] | null }>("/api/v1/template/get-templates").then((d) => d.templates ?? []),
    getTemplate: (id, revision) =>
      call<TemplateView>(`/api/v1/template/get-template${qs({ id, revision: revision ? String(revision) : undefined })}`),
    saveTemplate: (body) => call<SavedTemplate>("/api/v1/template/save-template", post(body)),
    deleteTemplate: async (id) => {
      await call<null>("/api/v1/template/delete-template", post({ id }));
    },
    listInstances: ({ serverId, templateId }) =>
      call<{ instances: InstanceView[] | null }>(
        `/api/v1/instance/get-instances${qs({ server_id: serverId, template_id: templateId })}`,
      ).then((d) => d.instances ?? []),
    getInstance,
    createInstance: (body) => command("/api/v1/instance/create-instance", body),
    updateInstance: (body) => command("/api/v1/instance/update-instance", body),
    upgradeInstance: (body) => command("/api/v1/instance/upgrade-instance", body),
    // An unlinked instance no longer answers to its key: a lost unlink
    // result stays the timeout.
    unlinkInstance: (serverId, key) =>
      command("/api/v1/instance/unlink-instance", { server_id: serverId, key }, "unlinked"),
    deleteInstance: (serverId, key) =>
      command("/api/v1/instance/delete-instance", { server_id: serverId, key }, "deleted"),
    publicKey: (serverId) =>
      call<{ public_key: string }>(`/api/v1/server/get-public-key${qs({ server_id: serverId })}`).then((d) => d.public_key),
  };
}

// NO_KEY is the API's answer for a server whose agent never reported its key.
const NO_KEY = "public key unavailable";

// sealSecrets encrypts the typed secret values ("" = not given, left out)
// for serverId with its current public key. Without any typed secret it
// reads no key. A server without a key yet gets an explanation instead of
// the API's terse refusal.
export async function sealSecrets(
  api: Pick<TemplatesApi, "publicKey">,
  serverId: string,
  typed: Record<string, string>,
): Promise<Record<string, string>> {
  const given = Object.entries(typed).filter(([, v]) => v !== "");
  if (given.length === 0) return {};
  let key: string;
  try {
    key = await api.publicKey(serverId);
  } catch (e) {
    if (e instanceof Error && e.message.includes(NO_KEY)) {
      throw new Error(
        "This server has no encryption key yet (its agent hasn't connected), so secrets can't be encrypted for it. Try again once it is online.",
      );
    }
    throw e;
  }
  const out: Record<string, string> = {};
  for (const [name, value] of given) out[name] = await encryptSecret(value, key);
  return out;
}
