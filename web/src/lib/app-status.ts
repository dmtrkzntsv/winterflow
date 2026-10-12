import { AppStatusCode } from "@/context/apps-context-base";
import type { BackupHealth } from "@/lib/backups";

// AppStatusWire is one app of an apps.status report as the API sends it
// (get-apps-status and the apps_status SSE event).
export type AppStatusWire = {
  app_id: string;
  status_code: number;
  // Shared variables whose rendered values differ from the agent's
  // current snapshot; empty/absent = fresh.
  stale_vars?: string[] | null;
  // The health of each of the app's backups (none: absent).
  backups?: BackupHealth[] | null;
};

// splitStatus turns a report into the apps context's three maps: container
// status codes, the stale shared names of stale apps only, and the backup
// health of apps with backups only.
export function splitStatus(apps: AppStatusWire[] | null | undefined): {
  status: Record<string, number>;
  stale: Record<string, string[]>;
  backups: Record<string, BackupHealth[]>;
} {
  const status: Record<string, number> = {};
  const stale: Record<string, string[]> = {};
  const backups: Record<string, BackupHealth[]> = {};
  for (const a of apps ?? []) {
    status[a.app_id] = a.status_code;
    if (a.stale_vars && a.stale_vars.length > 0) stale[a.app_id] = a.stale_vars;
    if (a.backups?.length) backups[a.app_id] = a.backups;
  }
  return { status, stale, backups };
}

// hasRunningContainers mirrors the agent's reload rule: a shared-variable
// reload applies only to an app with a running container, so a stopped app
// (or one without containers) picks its new values up on its next start.
export function hasRunningContainers(code: number | undefined): boolean {
  return code !== undefined && code !== AppStatusCode.Unknown && code !== AppStatusCode.Stopped;
}

// reloadableStaleCount counts the stale apps a server-wide reload acts on.
export function reloadableStaleCount(stale: Record<string, string[]>, status: Record<string, number>): number {
  return Object.entries(stale).filter(([id, names]) => names.length > 0 && hasRunningContainers(status[id])).length;
}
