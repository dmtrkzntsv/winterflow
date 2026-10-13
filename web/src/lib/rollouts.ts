// Rollouts: the API's queue of one-app-at-a-time jobs ("Upgrade all" of a
// template's instances, the reloads a shared-variable change asks for).
// Types mirror model.RolloutView; progress arrives as "rollout" events on
// the SSE stream, to every member of the org.
import { apiBase } from "@/config";
import { ApiError } from "@/lib/templates";
import { readApiError } from "@/lib/api";

export type RolloutKind = "template_upgrade" | "vars_reload";
export type RolloutState = "queued" | "running" | "done" | "stopped" | "failed";

export type Rollout = {
  id: string;
  kind: RolloutKind;
  scope_key: string; // the template id, or the org id for vars_reload
  revision: number; // template_upgrade: the target revision
  state: RolloutState;
  stop_requested: boolean;
  done: number;
  skipped: number;
  total: number; // done + skipped + the apps still to do
  current_app_id: string; // in flight; after a failure, the app that failed
  current_server_id: string;
  current_app_name: string;
  error: string;
  created_at: string;
  created_by: string;
  finished_at: string | null;
};

// ROLLOUT_EVENT is the SSE notification type of a rollout change.
export const ROLLOUT_EVENT = "rollout";

export function isActive(r: Rollout): boolean {
  return r.state === "queued" || r.state === "running";
}

// appLabel names the rollout's current app (its id's prefix until the API
// knows a name).
export function appLabel(r: Rollout): string {
  return r.current_app_name || r.current_app_id.slice(0, 8);
}

// rolloutLine is the strip's text for an active rollout: "Upgrading 3 of
// 12: bakomi…", "Reloading 2 of 5: shop on srv-2…".
export function rolloutLine(r: Rollout, serverName: (id: string) => string): string {
  const verb = r.kind === "template_upgrade" ? "Upgrading" : "Reloading";
  if (r.state === "queued" || !r.current_app_id) {
    return r.kind === "template_upgrade" ? `Upgrade to r${r.revision} queued…` : "Reload queued…";
  }
  const where = r.kind === "vars_reload" ? ` on ${serverName(r.current_server_id)}` : "";
  const step = Math.min(r.done + r.skipped + 1, Math.max(r.total, 1));
  const stopping = r.stop_requested ? " (stopping after this one)" : "…";
  return `${verb} ${step} of ${r.total}: ${appLabel(r)}${where}${stopping}`;
}

// failureLine names what stopped a failed rollout: the app it stopped at,
// or just that it failed when no app was in flight (its error says why).
export function failureLine(r: Rollout, serverName: (id: string) => string): string {
  const what = r.kind === "template_upgrade" ? `Upgrade to r${r.revision}` : "Reload";
  if (!r.current_app_id) return `${what} failed`;
  const where = r.kind === "vars_reload" && r.current_server_id ? ` on ${serverName(r.current_server_id)}` : "";
  return `${what} stopped at ${appLabel(r)}${where}`;
}

export type RolloutsApi = {
  // list reads the newest rollouts of a kind (the active one first); for
  // vars_reload the scope is the caller's org.
  list(kind: RolloutKind, scopeKey?: string): Promise<Rollout[]>;
  stop(id: string): Promise<Rollout>;
  // upgradeAll queues "Upgrade all" of a template's instances to revision
  // (an ApiError with status 409 while one is active).
  upgradeAll(templateId: string, revision: number): Promise<Rollout>;
};

export function createRolloutsApi(fetchImpl?: typeof fetch): RolloutsApi {
  const doFetch: typeof fetch = fetchImpl ?? ((input, init) => fetch(input, init));
  async function call<T>(path: string, init?: RequestInit): Promise<T> {
    const res = await doFetch(`${apiBase}${path}`, { credentials: "include", ...init });
    if (!res.ok) throw new ApiError((await readApiError(res)).message, res.status);
    return ((await res.json()) as { data: T }).data;
  }
  const post = (payload: unknown): RequestInit => ({
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(payload),
  });
  return {
    list: (kind, scopeKey) => {
      const q = new URLSearchParams({ kind });
      if (scopeKey) q.set("scope_key", scopeKey);
      return call<{ rollouts: Rollout[] | null }>(`/api/v1/rollout/get-rollouts?${q}`).then((d) => d.rollouts ?? []);
    },
    stop: (id) => call<Rollout>("/api/v1/rollout/stop-rollout", post({ id })),
    upgradeAll: (templateId, revision) =>
      call<Rollout>("/api/v1/template/upgrade-instances", post({ id: templateId, revision })),
  };
}
