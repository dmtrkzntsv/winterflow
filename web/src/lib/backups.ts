// Backups: what an app's compose file declares under x-winterflow.backups
// (the recipe), the app's own settings on top (enabled, storage, schedule,
// retention, location — the overrides), and the agent's copies (restore
// points). Mirrors internal/domain/backup/recipe and
// internal/domain/command/backup.go; the agent checks everything again.

export type BackupType = "sqlite" | "files" | "custom";

export type BackupState = "off" | "running" | "never" | "ok" | "late" | "failing";

// daily and monthly come back null (not absent) when the tier is unset.
export type Keep = { recent?: string; daily?: number | null; monthly?: number | null };

// RecipeField is a field the recipe may set and an override may change.
export type RecipeField = "every" | "keep" | "volume" | "service" | "path" | "paths" | "exclude";

// EffectiveBackup is a backup as it runs: the recipe, the overrides on top,
// the defaults applied. from_recipe marks the fields holding the recipe's
// value; in_recipe is false for a backup added in the UI.
export type EffectiveBackup = {
  name: string;
  type: BackupType;
  enabled: boolean;
  storage: string;
  every?: string;
  keep?: Keep | null;
  volume?: string;
  service?: string;
  path?: string;
  paths?: string[] | null;
  exclude?: string[] | null;
  in_recipe: boolean;
  from_recipe: Partial<Record<RecipeField, boolean>> | null;
};

// BackupConfig is one entry of the list update-backups replaces: an
// override of a recipe backup (only what it changes, never type) or a
// backup added in the UI (type and every field it needs).
export type BackupConfig = {
  name: string;
  type?: BackupType;
  enabled?: boolean;
  storage?: string;
  every?: string;
  keep?: Keep;
  volume?: string;
  service?: string;
  path?: string;
  paths?: string[];
  exclude?: string[];
};

// BackupHealth is one backup's health in apps.status.
export type BackupHealth = {
  name: string;
  state: BackupState;
  last_success?: number; // unix seconds
  last_error?: string;
};

export type PointKind = "scheduled" | "manual" | "pre-restore";

// RestorePoint is one stored copy. time and expires are unix seconds
// (expires 0/absent: never); size is the stored size in bytes.
export type RestorePoint = { id: string; time: number; kind: PointKind; size: number; expires?: number };

export type RunBackupResult = { unchanged: boolean; point?: RestorePoint | null; warnings: string[] };

export type RestoreResult = { restored_point: string; pre_restore_point?: string; warnings: string[] };

// Orphan is an app's backup folder in a storage whose app is not on this
// server (times unix seconds; purge_at 0: never deleted automatically).
export type Orphan = {
  app_id: string;
  app_name?: string;
  deleted_at?: number;
  purge_at?: number;
  copies: number;
  bytes: number;
  newest?: number;
};

// Mount is a service's volume or bind mount as its compose file writes it.
export type Mount = { type: "volume" | "bind"; source: string; target: string };

// BackupTargets are where a backup added in the UI can point.
export type BackupTargets = { volumes: string[]; services: { name: string; mounts: Mount[] }[] };

export const MANUAL = "manual";

// SCHEDULES are the schedule picker's choices; a value outside them (set in
// the compose file) is shown as is.
export const SCHEDULES: { value: string; label: string }[] = [
  { value: MANUAL, label: "Manual" },
  { value: "15m", label: "Every 15 minutes" },
  { value: "30m", label: "Every 30 minutes" },
  { value: "1h", label: "Every hour" },
  { value: "6h", label: "Every 6 hours" },
  { value: "12h", label: "Every 12 hours" },
  { value: "24h", label: "Every day" },
];

// durationMs parses a Go duration ("1h30m", "15m0s", "24h0m0s"); NaN when
// it isn't one.
export function durationMs(d: string): number {
  const re = /(\d+(?:\.\d+)?)(ms|h|m|s)/g;
  const unit: Record<string, number> = { h: 3_600_000, m: 60_000, s: 1000, ms: 1 };
  let total = 0;
  let consumed = 0;
  for (const m of d.matchAll(re)) {
    total += parseFloat(m[1]) * unit[m[2]];
    consumed += m[0].length;
  }
  return d && consumed === d.length ? total : NaN;
}

// scheduleLabel reads a backup's every: "Manual", a picker label, or
// "Every <duration>".
export function scheduleLabel(every: string | undefined): string {
  if (!every || every === MANUAL) return "Manual";
  const ms = durationMs(every);
  const known = SCHEDULES.find((s) => s.value !== MANUAL && durationMs(s.value) === ms);
  return known ? known.label : `Every ${every}`;
}

// everyError mirrors the recipe's rule: manual, or a duration of at least
// 5 minutes; "" means valid.
export function everyError(every: string): string {
  if (every === MANUAL) return "";
  const ms = durationMs(every);
  if (Number.isNaN(ms)) return "Use a duration like 15m, 2h or 24h, or Manual.";
  return ms < 5 * 60_000 ? "Back up at most every 5 minutes." : "";
}

// changed reports whether a field holds an override (not the recipe's value).
export function changed(b: EffectiveBackup, field: RecipeField): boolean {
  return b.in_recipe && !b.from_recipe?.[field];
}

// configOf is the list entry that keeps b as it is: a UI-added backup in
// full, a recipe backup's on/off, storage and the fields it overrides.
export function configOf(b: EffectiveBackup): BackupConfig {
  const c: BackupConfig = { name: b.name };
  if (b.enabled) c.enabled = true;
  if (b.storage) c.storage = b.storage;
  const take = (f: RecipeField) => !b.in_recipe || changed(b, f);
  if (!b.in_recipe) c.type = b.type;
  if (b.type === "custom") {
    if (!b.in_recipe && b.service) c.service = b.service;
    return c;
  }
  if (take("every") && b.every) c.every = b.every;
  if (take("keep") && b.keep) c.keep = { ...b.keep };
  // The location moves as a whole: volume or service, and its paths.
  if (take("volume") || take("service")) {
    if (b.volume) c.volume = b.volume;
    if (b.service) c.service = b.service;
  }
  if (take("path") && b.path) c.path = b.path;
  // An explicit empty list (the whole volume, no excludes) comes back
  // absent: an override of it is sent as [] again, never invented.
  if (b.type === "files") {
    if (take("paths") && (b.paths || changed(b, "paths"))) c.paths = [...(b.paths ?? [])];
    if (take("exclude") && (b.exclude || changed(b, "exclude"))) c.exclude = [...(b.exclude ?? [])];
  }
  return c;
}

// configsWith is the whole list update-backups takes: every backup kept as
// it is, but name, which is replaced by next (null: removed — only a
// UI-added backup can go). A name not in the list is appended (Add backup).
// Entries that say nothing beyond their name are left out.
export function configsWith(backups: EffectiveBackup[], name: string, next: BackupConfig | null): BackupConfig[] {
  const out: BackupConfig[] = [];
  let found = false;
  for (const b of backups) {
    if (b.name === name) {
      found = true;
      if (next) out.push(next);
      continue;
    }
    out.push(configOf(b));
  }
  if (!found && next) out.push(next);
  return out.filter((c) => Object.keys(c).length > 1);
}

// resetField drops a field's override from a backup's entry, so it follows
// the recipe again. The location fields go together.
export function resetField(c: BackupConfig, field: RecipeField): BackupConfig {
  const next = { ...c };
  const drop: RecipeField[] =
    field === "volume" || field === "service" || field === "path" || field === "paths" ? ["volume", "service", "path", "paths"] : [field];
  for (const f of drop) delete next[f];
  return next;
}

// keepDays is how far back a retention reaches, in days: the recent window,
// the daily tier and the monthly one (31 days a month).
export function keepDays(keep: Keep | null | undefined): number {
  const recent = durationMs(keep?.recent || "24h") / 86_400_000;
  return Math.max(recent || 1, keep?.daily ?? 14, (keep?.monthly ?? 6) * 31);
}

// cappedNote is the note under a retention that reaches past the server's
// ceiling (retention days, 0 = none), else "".
export function cappedNote(keep: Keep | null | undefined, ceiling: number): string {
  return ceiling > 0 && keepDays(keep) > ceiling ? `Capped at ${ceiling} days on this server.` : "";
}

// keepLabel reads a retention: "24h, 14 daily, 6 monthly".
export function keepLabel(keep: Keep | null | undefined): string {
  // Go writes 24h as 24h0m0s: drop the zero minutes and seconds.
  const recent = keep?.recent ? keep.recent.replace(/(\d)m0s$/, "$1m").replace(/h0m$/, "h") : "24h";
  return `${recent}, ${keep?.daily ?? 14} daily, ${keep?.monthly ?? 6} monthly`;
}

// locationLabel reads where a backup's data is: "volume data: app.db" or
// "service web: /app/media, /app/config"; a whole volume reads "volume
// data (all of it)".
export function locationLabel(b: Pick<EffectiveBackup, "type" | "volume" | "service" | "path" | "paths">): string {
  const where = b.volume ? `volume ${b.volume}` : b.service ? `service ${b.service}` : "";
  if (b.type === "custom") return where;
  const paths = b.type === "sqlite" ? (b.path ? [b.path] : []) : (b.paths ?? []);
  if (paths.length === 0) return b.volume ? `${where} (all of it)` : where;
  return `${where}: ${paths.join(", ")}`;
}

// relativeAge reads a unix time as "4 min ago" (now in ms).
export function relativeAge(unix: number, now: number = Date.now()): string {
  const secs = Math.max(0, Math.round(now / 1000 - unix));
  if (secs < 60) return "just now";
  if (secs < 3600) return `${Math.floor(secs / 60)} min ago`;
  if (secs < 86_400) return `${Math.floor(secs / 3600)} h ago`;
  const days = Math.floor(secs / 86_400);
  return days === 1 ? "yesterday" : `${days} days ago`;
}

// formatBytes reads a size: "512 B", "1.5 MB".
export function formatBytes(n: number): string {
  if (n < 1000) return `${n} B`;
  const units = ["kB", "MB", "GB", "TB"];
  let v = n / 1000;
  let i = 0;
  while (v >= 1000 && i < units.length - 1) {
    v /= 1000;
    i++;
  }
  return `${v.toFixed(v < 10 ? 1 : 0)} ${units[i]}`;
}

// expiresNote is "expires in N days" for a copy within 14 days of the
// server's ceiling (now in ms), else "".
export function expiresNote(p: RestorePoint, now: number = Date.now()): string {
  if (!p.expires) return "";
  const days = Math.ceil((p.expires * 1000 - now) / 86_400_000);
  if (days > 14) return "";
  if (days <= 0) return "expires today";
  return days === 1 ? "expires in 1 day" : `expires in ${days} days`;
}

export const KIND_LABELS: Record<PointKind, string> = {
  scheduled: "Scheduled",
  manual: "Manual",
  "pre-restore": "Before a restore",
};

export const STATE_LABELS: Record<BackupState, string> = {
  off: "Off",
  running: "Running",
  never: "Not run yet",
  ok: "OK",
  late: "Late",
  failing: "Failing",
};

// needsAttention reports whether an app's backups include a late or failing
// one (the app grid's warning dot).
export function needsAttention(health: BackupHealth[] | undefined): boolean {
  return (health ?? []).some((h) => h.state === "late" || h.state === "failing");
}

const NAME_RE = /^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$/;

// backupNameError mirrors recipe.ValidateName plus uniqueness in the app;
// "" means valid.
export function backupNameError(name: string, taken: readonly string[]): string {
  if (!NAME_RE.test(name)) return "Use 1-32 lowercase letters, digits and inner hyphens, like database.";
  return taken.includes(name) ? `The app already has a backup named ${name}.` : "";
}
