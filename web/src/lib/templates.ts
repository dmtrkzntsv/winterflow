// Templates and their instances: types mirroring the API
// (internal/domain/template, internal/domain/usecase/templates) and the API
// client. A template is an org's own (editable) or the catalog's (the same
// for every org, read-only); an instance is an app on one server rendered
// from a template revision. What an instance fills in, which ports it
// exposes and which backups it can turn on come from the compose files'
// x-winterflow block (detected). Secret values travel only as ECIES
// ciphertexts for the instance's server (lib/ecies); no route ever returns
// one.
import { apiBase } from "@/config";
import { WaitTimeoutError } from "@/context/notifications-context-base";
import { createJsonClient, jsonPost } from "@/lib/api";
import { encryptSecret } from "@/lib/ecies";
import type { DeclaredVariable } from "@/types/app-config";

// TemplateBody is a revision's body: the app's files and how the template
// reads in a list. Go encodes an empty map as null.
export type TemplateBody = {
  files: Record<string, string> | null;
  summary: string;
  description: string;
  icon: string;
  color: string;
};

export type DetectedExpose = {
  id: string; // <container>:<port>/<protocol>
  target: string; // <container>:<port>: what a hostname's expose names
  container: string;
  port: number;
  protocol: "http" | "tcp" | "udp";
};

export type DetectedBackup = { name: string; type: "sqlite" | "files" | "custom" };

// Detected is what a body's compose files declare under x-winterflow.
export type Detected = {
  variables: DeclaredVariable[] | null;
  expose: DetectedExpose[] | null;
  backups: DetectedBackup[] | null;
  warnings: string[] | null;
};

// Inspection is inspect-template's answer: what a body declares (as far
// as it parses) and the first problem a save would refuse ("" when none).
export type Inspection = { detected: Detected; problem: string };

export type TemplateSource = "org" | "catalog";

// CatalogMeta is what the catalog says about a catalog template besides its
// name and files (null for an org template). Go encodes an empty list as
// null.
export type CatalogMeta = {
  summary: string;
  description: string;
  keywords: string[] | null;
  tags: string[] | null;
  alternative_to: string[] | null; // apps this one replaces, as search words
  license: string;
  homepage_url: string;
  repository_url: string;
  documentation_url: string;
  logo_url: string;
};

export type TemplateSummary = {
  id: string;
  source: TemplateSource;
  slug: string; // a catalog template's id in the catalog
  name: string;
  latest_revision: number;
  instances: number; // the org's pending and created ones
  behind: number; // of those, on a revision older than the latest
  withdrawn_at: string | null; // when the catalog dropped it
  catalog?: CatalogMeta | null; // a catalog template's metadata; null for an org one
  updated_at: string;
};

// orgTemplates are the org's own templates of a list (the catalog's are
// listed after them).
export function orgTemplates<T extends Pick<TemplateSummary, "source">>(list: T[]): T[] {
  return list.filter((t) => t.source === "org");
}

// usageLabel says how many apps use a template, in plain words: "Used by
// 12 apps · 3 on an older version" ("Not used by any app yet" without any).
export function usageLabel(t: Pick<TemplateSummary, "instances" | "behind">): string {
  if (t.instances === 0) return "Not used by any app yet";
  const used = `Used by ${t.instances} ${t.instances === 1 ? "app" : "apps"}`;
  return t.behind > 0 ? `${used} · ${t.behind} on an older version` : used;
}

export type TemplateRevisionInfo = { revision: number; note: string; created_at: string; created_by: string };

export type TemplateView = {
  id: string;
  source: TemplateSource;
  slug: string;
  name: string;
  latest_revision: number;
  withdrawn_at: string | null;
  catalog?: CatalogMeta | null;
  revision: number;
  body: TemplateBody;
  detected: Detected;
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

// InstanceHost routes a hostname to an http expose entry (its target).
export type InstanceHost = { host: string; expose: string };

// BackupChoice is an instance's setting for one backup its compose file
// declares; a backup without one starts off.
export type BackupChoice = { enabled: boolean; storage?: string };

export type InstanceView = {
  key: string;
  server_id: string;
  app_id: string;
  template_id: string;
  template_name: string;
  revision: number;
  name: string; // the app name the create asked for
  values: Record<string, string> | null; // plain values callers set
  secrets: Record<string, string> | null; // name -> "<encrypted>"
  hosts: InstanceHost[] | null;
  state: InstanceState;
  status_code: number;
  last_error: string;
  created_at: string;
  updated_at: string;
};

// InstanceValues are what create, update and upgrade send: plain values,
// secret ciphertexts, hostnames and (while pending) backup settings.
export type InstanceValues = {
  values?: Record<string, string>;
  secrets?: Record<string, string>;
  hosts?: InstanceHost[];
  backups?: Record<string, BackupChoice>;
};

export type CreateInstanceBody = InstanceValues & {
  server_id: string;
  key: string;
  template_id: string;
  revision?: number;
  name?: string;
};

export type UpdateInstanceBody = InstanceValues & { server_id: string; key: string };

export type UpgradeInstanceBody = UpdateInstanceBody & { revision: number };

// INSTANCE_WAIT_MS bounds the wait for an instance command's result over
// SSE: a deploy may pull an image for minutes (the API's own wait bound).
export const INSTANCE_WAIT_MS = 10 * 60_000;

// ApiError is a refused request with its HTTP status (lib/api).
export { ApiError } from "@/lib/api";

// MANAGED_MARKER starts the API's 409 to save-app and rollback-app on an
// app a template instance manages (model.ManagedByTemplate).
const MANAGED_MARKER = "this app is managed by ";

export function isManagedByTemplate(message: string): boolean {
  return message.includes(MANAGED_MARKER);
}

export type WaitFor = (ref: string, timeoutMs?: number) => Promise<{ status?: number; error?: string }>;

// LOAD_TIMEOUT_MS bounds every read. Over HTTP/1.1 a browser opens at most
// six connections to a host, and every open Winterflow tab holds one for its
// event stream: with enough tabs open a read is queued and never answered,
// and only a deadline turns that into an error (and aborts the request,
// freeing its place in the browser's queue).
export const LOAD_TIMEOUT_MS = 20_000;
export const TOO_LONG =
  "The server didn't answer in time. Many open Winterflow tabs can use up the browser's connections to it: close some, then retry.";

export type TemplatesApi = {
  // Every read fails with TOO_LONG after LOAD_TIMEOUT_MS.
  listTemplates(): Promise<TemplateSummary[]>;
  // getTemplate reads the latest revision, or the given one (its
  // latest_revision and revisions as of now).
  getTemplate(id: string, revision?: number): Promise<TemplateView>;
  // getRevision reads a revision for its body, which never changes: a
  // client reads each one once (the rest of the view is as of that read).
  getRevision(id: string, revision: number): Promise<TemplateView>;
  saveTemplate(body: SaveTemplateBody): Promise<SavedTemplate>;
  // inspectTemplate reads a body as a save would, storing nothing.
  inspectTemplate(body: TemplateBody): Promise<Inspection>;
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
  const { call, request } = createJsonClient(apiBase, fetchImpl);
  // read is a GET with the deadline: past it the request is aborted and the
  // read fails with TOO_LONG (also when the fetch ignores the abort).
  async function read<T>(path: string): Promise<T> {
    const ctl = new AbortController();
    let timer: ReturnType<typeof setTimeout> | undefined;
    const deadline = new Promise<never>((_, reject) => {
      timer = setTimeout(() => {
        reject(new Error(TOO_LONG));
        ctl.abort();
      }, LOAD_TIMEOUT_MS);
    });
    try {
      return await Promise.race([call<T>(path, { signal: ctl.signal }), deadline]);
    } finally {
      clearTimeout(timer);
    }
  }
  // command posts an instance call without wait (lib/api.ts's
  // dispatchCommand is the general variant): a 202 names the request
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
    const res = await request(path, jsonPost(payload));
    if (res.status !== 202) return;
    const ref = ((await res.json()) as { data?: { request_id?: string } }).data?.request_id;
    if (!ref) throw new Error("No request id");
    let result: Awaited<ReturnType<WaitFor>>;
    try {
      result = await waitFor(ref, INSTANCE_WAIT_MS);
    } catch (e) {
      if (!(e instanceof WaitTimeoutError)) throw e;
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
    read<InstanceView>(`/api/v1/instance/get-instance${qs({ server_id: serverId, key })}`);
  const getTemplate = (id: string, revision?: number) =>
    read<TemplateView>(`/api/v1/template/get-template${qs({ id, revision: revision ? String(revision) : undefined })}`);
  // revisions holds getRevision's reads by id and revision; a failed one is
  // dropped so the next call reads again.
  const revisions = new Map<string, Promise<TemplateView>>();
  const getRevision = (id: string, revision: number) => {
    const k = `${id}@${revision}`;
    let p = revisions.get(k);
    if (!p) {
      p = getTemplate(id, revision);
      revisions.set(k, p);
      p.catch(() => revisions.delete(k));
    }
    return p;
  };
  return {
    listTemplates: () =>
      read<{ templates: TemplateSummary[] | null }>("/api/v1/template/get-templates").then((d) => d.templates ?? []),
    getTemplate,
    getRevision,
    saveTemplate: (body) => call<SavedTemplate>("/api/v1/template/save-template", jsonPost(body)),
    inspectTemplate: (body) => call<Inspection>("/api/v1/template/inspect-template", jsonPost({ body })),
    deleteTemplate: async (id) => {
      await call<null>("/api/v1/template/delete-template", jsonPost({ id }));
    },
    listInstances: ({ serverId, templateId }) =>
      read<{ instances: InstanceView[] | null }>(
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
      read<{ public_key: string }>(`/api/v1/server/get-public-key${qs({ server_id: serverId })}`).then((d) => d.public_key),
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
  return Object.fromEntries(
    await Promise.all(given.map(async ([name, value]) => [name, await encryptSecret(value, key)] as const)),
  );
}
