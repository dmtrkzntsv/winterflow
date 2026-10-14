import { AppStatusCode } from "@/context/apps-context-base";
import type { ServerLiveness } from "@/context/servers-context-base";
import { STATE_LABELS, type BackupState } from "@/lib/backups";

// StatusTone is the colour family of a state: one token each in index.css
// (--status-ok|warn|bad|idle), so the same state looks the same on every
// page.
export type StatusTone = "ok" | "warn" | "bad" | "idle";

// StatusInfo is a state as the UI shows it: the tone, the word, and whether
// it is a moving state (pulses).
export type StatusInfo = { tone: StatusTone; label: string; pulse?: boolean };

export const TONE_DOT_CLASS: Record<StatusTone, string> = {
  ok: "bg-status-ok",
  warn: "bg-status-warn",
  bad: "bg-status-bad",
  idle: "bg-status-idle",
};

export const TONE_PILL_CLASS: Record<StatusTone, string> = {
  ok: "bg-status-ok/15 text-foreground",
  warn: "bg-status-warn/15 text-foreground",
  bad: "bg-status-bad/15 text-foreground",
  idle: "bg-muted text-muted-foreground",
};

const APP: Record<number, StatusInfo> = {
  [AppStatusCode.Active]: { tone: "ok", label: "Running" },
  [AppStatusCode.Idle]: { tone: "warn", label: "Idle" },
  [AppStatusCode.Restarting]: { tone: "warn", label: "Restarting", pulse: true },
  [AppStatusCode.Problematic]: { tone: "bad", label: "Problematic" },
  [AppStatusCode.Stopped]: { tone: "idle", label: "Stopped" },
  [AppStatusCode.Unknown]: { tone: "idle", label: "Unknown" },
};

// FAILED is an app whose deploy failed (its create's error), until a later
// report shows it running.
export const FAILED: StatusInfo = { tone: "bad", label: "Failed" };

// appStatus maps a container status code to its tone and word; failed (a
// failed deploy the apps context holds) reads Failed. A missing code is
// Unknown: the UI never infers "down" from missing data.
export function appStatus(code: number | undefined, failed = false): StatusInfo {
  if (failed) return FAILED;
  return APP[code ?? AppStatusCode.Unknown] ?? APP[AppStatusCode.Unknown];
}

// serverStatus says whether a server's agent is connected: live liveness
// first, then whether it ever reported in. A standalone install's server
// is the process itself.
export function serverStatus({
  liveness,
  lastSeenAt,
  standalone,
}: {
  liveness?: ServerLiveness;
  lastSeenAt: string | null;
  standalone: boolean;
}): StatusInfo {
  if (standalone) return { tone: "ok", label: "This server" };
  if (liveness === "online") return { tone: "ok", label: "Online" };
  if (!lastSeenAt) return { tone: "idle", label: "Never connected" };
  return { tone: "bad", label: "Offline" };
}

const BACKUP_TONE: Record<BackupState, Omit<StatusInfo, "label">> = {
  ok: { tone: "ok" },
  running: { tone: "ok", pulse: true },
  never: { tone: "idle" },
  off: { tone: "idle" },
  late: { tone: "warn" },
  failing: { tone: "bad" },
};

// backupStatus maps a backup's health state to its tone and word.
export function backupStatus(state: BackupState): StatusInfo {
  return { ...BACKUP_TONE[state], label: STATE_LABELS[state] };
}

// RESTORING is a backup's state while it is being restored (the app is
// stopped meanwhile).
export const RESTORING: StatusInfo = { tone: "warn", label: "Restoring", pulse: true };
