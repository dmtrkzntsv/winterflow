// Rollouts: the API's queue of one-app-at-a-time jobs ("Update all apps" of a
// template, the reloads a shared-variable change asks for).
// Types mirror model.RolloutView; progress arrives as "rollout" events on
// the SSE stream, to every member of the org.
import { apiBase } from "@/config";
import { createJsonClient, jsonPost } from "@/lib/api";
import { appLabel } from "@/lib/kpi";

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

// currentApp names the rollout's current app.
export function currentApp(r: Rollout): string {
  return appLabel(r.current_app_name);
}

// rolloutLine is the strip's text for an active rollout: "Updating 3 of
// 12: bakomi…", "Reloading 2 of 5: shop on srv-2…".
export function rolloutLine(r: Rollout, serverName: (id: string) => string): string {
  const verb = r.kind === "template_upgrade" ? "Updating" : "Reloading";
  if (r.state === "queued" || !r.current_app_id) {
    return r.kind === "template_upgrade" ? `Update to version ${r.revision} queued…` : "Reload queued…";
  }
  const where = r.kind === "vars_reload" ? ` on ${serverName(r.current_server_id)}` : "";
  const step = Math.min(r.done + r.skipped + 1, Math.max(r.total, 1));
  const stopping = r.stop_requested ? " (stopping after this one)" : "…";
  return `${verb} ${step} of ${r.total}: ${currentApp(r)}${where}${stopping}`;
}

// failureLine names what stopped a failed rollout: the app it stopped at,
// or just that it failed when no app was in flight (its error says why).
export function failureLine(r: Rollout, serverName: (id: string) => string): string {
  const what = r.kind === "template_upgrade" ? `Update to version ${r.revision}` : "Reload";
  if (!r.current_app_id) return `${what} failed`;
  const where = r.kind === "vars_reload" && r.current_server_id ? ` on ${serverName(r.current_server_id)}` : "";
  return `${what} stopped at ${currentApp(r)}${where}`;
}

const apps = (n: number) => `${n} app${n === 1 ? "" : "s"}`;

// skippedLine says what a finished or stopped "Update all apps" left behind:
// "Updated 2 apps to version 5 · 1 skipped: it needs a new value — update it
// on its own." Null for any other rollout, or when nothing was skipped.
export function skippedLine(r: Rollout): string | null {
  if (r.kind !== "template_upgrade" || (r.state !== "done" && r.state !== "stopped") || r.skipped <= 0) return null;
  const updated =
    r.state === "stopped"
      ? r.done > 0
        ? `Stopped after updating ${apps(r.done)} to version ${r.revision}`
        : `Stopped before updating any app to version ${r.revision}`
      : r.done > 0
        ? `Updated ${apps(r.done)} to version ${r.revision}`
        : `No app updated to version ${r.revision}`;
  const why = r.skipped === 1 ? "it needs a new value — update it on its own" : "they need a new value — update them on their own";
  return `${updated} · ${r.skipped} skipped: ${why}.`;
}

export type RolloutsApi = {
  // list reads the newest rollouts of a kind (the active one first); for
  // vars_reload the scope is the caller's org.
  list(kind: RolloutKind, scopeKey?: string): Promise<Rollout[]>;
  stop(id: string): Promise<Rollout>;
  // upgradeAll queues "Update all apps" of a template to revision
  // (an ApiError with status 409 while one is active).
  upgradeAll(templateId: string, revision: number): Promise<Rollout>;
};

export function createRolloutsApi(fetchImpl?: typeof fetch): RolloutsApi {
  const { call } = createJsonClient(apiBase, fetchImpl);
  return {
    list: (kind, scopeKey) => {
      const q = new URLSearchParams({ kind });
      if (scopeKey) q.set("scope_key", scopeKey);
      return call<{ rollouts: Rollout[] | null }>(`/api/v1/rollout/get-rollouts?${q}`).then((d) => d.rollouts ?? []);
    },
    stop: (id) => call<Rollout>("/api/v1/rollout/stop-rollout", jsonPost({ id })),
    upgradeAll: (templateId, revision) =>
      call<Rollout>("/api/v1/template/upgrade-instances", jsonPost({ id: templateId, revision })),
  };
}
