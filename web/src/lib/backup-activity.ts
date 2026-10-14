import { useSyncExternalStore } from "react";

import { nextCopyNote, type BackupHealth, type BackupState, type EffectiveBackup } from "@/lib/backups";

// Back up now and Restore answer over SSE when they end, up to ~2 hours
// later. The page must stay usable meanwhile, and switching tabs must not
// forget a run in flight, so what is running lives here, not in a component.

export type Activity = "backup" | "restore";

// createStore is a value with subscribers, for useSyncExternalStore. set
// takes a new object per change: snapshots are compared by identity.
function createStore<T>(initial: T) {
  let value = initial;
  const listeners = new Set<() => void>();
  return {
    get: () => value,
    set(next: T) {
      value = next;
      for (const l of listeners) l();
    },
    subscribe(l: () => void) {
      listeners.add(l);
      return () => {
        listeners.delete(l);
      };
    },
  };
}

// What is running, by app id then backup name: every run in flight (a
// Back up now and a restore of one backup can overlap, each ends on its
// own).
type Running = Readonly<Record<string, readonly Activity[]>>;
type ActivityMap = Readonly<Record<string, Running>>;

const activity = createStore<ActivityMap>({});

function change(appId: string, backup: string, update: (kinds: readonly Activity[]) => readonly Activity[]) {
  const snapshot = activity.get();
  const app = { ...snapshot[appId] };
  const kinds = update(app[backup] ?? []);
  if (kinds.length > 0) app[backup] = kinds;
  else delete app[backup];
  const next = { ...snapshot, [appId]: app };
  if (Object.keys(app).length === 0) delete (next as Record<string, unknown>)[appId];
  activity.set(next);
}

// trackActivity marks a backup as running and returns the function that
// clears that mark only (call it when the result arrives, success or not).
export function trackActivity(appId: string, backup: string, kind: Activity): () => void {
  change(appId, backup, (kinds) => [...kinds, kind]);
  let ended = false;
  return () => {
    if (ended) return;
    ended = true;
    change(appId, backup, (kinds) => {
      const i = kinds.indexOf(kind);
      return i < 0 ? kinds : kinds.filter((_, j) => j !== i);
    });
  };
}

const NONE: Running = {};

// useActivity is what is running for an app: backup name -> its runs.
export function useActivity(appId: string): Running {
  return useSyncExternalStore(
    activity.subscribe,
    () => activity.get()[appId] ?? NONE,
    () => NONE,
  );
}

// isRunning: the backup has a run of that kind in flight.
export function isRunning(running: Running, backup: string, kind: Activity): boolean {
  return running[backup]?.includes(kind) ?? false;
}

// restoringBackup is the name of the backup being restored, if any (a
// restore stops the app: the whole app is busy).
export function restoringBackup(running: Running): string | undefined {
  return Object.entries(running).find(([, kinds]) => kinds.includes("restore"))?.[0];
}

// backupRowState is what a backup's row says about it, for the Backups tab
// and the Overview alike: its state (a run started here reads running before
// the agent reports it), whether it is being restored, whether a run is
// under way (here, or reported by the agent: a second request would be
// refused), and the next-copy note (empty while a run or restore is under
// way, and for a custom backup, which winterflow doesn't schedule).
export function backupRowState(
  b: Pick<EffectiveBackup, "enabled" | "type" | "every">,
  health: BackupHealth | undefined,
  kinds: readonly Activity[],
): { state: BackupState; restoring: boolean; running: boolean; note: string } {
  const restoring = kinds.includes("restore");
  const state: BackupState = kinds.includes("backup") ? "running" : (health?.state ?? (b.enabled ? "never" : "off"));
  const running = kinds.includes("backup") || health?.state === "running";
  const note = restoring || running || b.type === "custom" ? "" : nextCopyNote(b, state, health?.last_success);
  return { state, restoring, running, note };
}

// What the user was just told (a toast) must not be contradicted by a
// health snapshot that is older than it: apps_status carries no timestamp,
// and the agent's report can trail the operation by a moment. So the result
// of an operation is held here as the backup's health, and given up only
// once a snapshot shows its effect (or after OVERLAY_HOLD_MS, so a
// snapshot that never comes can't pin a state forever). The price: a run that
// starts elsewhere within that time shows only once it is over.

// An Outcome is what an operation on a backup achieved.
export type Outcome =
  | { kind: "backed-up"; unchanged: boolean }
  | { kind: "failed"; error: string }
  | { kind: "off" }
  | { kind: "on" };

export const OVERLAY_HOLD_MS = 90_000;

export type Overlay = { health: BackupHealth; shows: (h: BackupHealth | undefined) => boolean; until: number };

// overlayFor is the health that follows an outcome, and the snapshot that
// shows it: prior is the health before the operation (a snapshot with the
// same last success is older than a copy just made).
export function overlayFor(name: string, outcome: Outcome, prior: BackupHealth | undefined, now: number = Date.now()): Overlay {
  const until = now + OVERLAY_HOLD_MS;
  const before = prior?.last_success ?? 0;
  switch (outcome.kind) {
    case "backed-up":
      return {
        health: { name, state: "ok", last_success: Math.floor(now / 1000) },
        // A copy was made: the snapshot shows a newer last success. An
        // unchanged run may leave it as it was.
        shows: (h) => h?.state === "ok" && (outcome.unchanged ? (h.last_success ?? 0) >= before : (h.last_success ?? 0) > before),
        until,
      };
    case "failed":
      return {
        health: { name, state: "failing", last_success: prior?.last_success, last_error: outcome.error },
        shows: (h) => h?.state === "failing" || (h?.state === "ok" && (h.last_success ?? 0) > before),
        until,
      };
    case "off":
      return { health: { name, state: "off" }, shows: (h) => !h || h.state === "off", until };
    case "on":
      return { health: { name, state: "never" }, shows: (h) => !!h && h.state !== "off", until };
  }
}

// shownHealth is the health to show: the snapshot, unless the overlay still
// holds something newer.
export function shownHealth(overlay: Overlay | undefined, snapshot: BackupHealth | undefined, now: number = Date.now()): BackupHealth | undefined {
  if (!overlay || now >= overlay.until || overlay.shows(snapshot)) return snapshot;
  return overlay.health;
}

type Overlays = Readonly<Record<string, Readonly<Record<string, Overlay>>>>;
const overlayStore = createStore<Overlays>({});

// holdHealth records an operation's outcome for a backup (it outlives the
// tab, like the activity above).
export function holdHealth(appId: string, backup: string, outcome: Outcome, prior: BackupHealth | undefined, now: number = Date.now()) {
  const overlays = overlayStore.get();
  const app = { ...overlays[appId], [backup]: overlayFor(backup, outcome, prior, now) };
  for (const [name, o] of Object.entries(app)) if (now >= o.until) delete app[name];
  overlayStore.set({ ...overlays, [appId]: app });
}

// releaseHealth gives a held health up (a report has shown its effect, or its
// time is over): from then on the reports are followed again, even one that
// differs.
export function releaseHealth(appId: string, backup: string) {
  const overlays = overlayStore.get();
  const app = overlays[appId];
  if (!app?.[backup]) return;
  const rest = { ...app };
  delete rest[backup];
  overlayStore.set({ ...overlays, [appId]: rest });
}

const NO_OVERLAYS: Readonly<Record<string, Overlay>> = {};

// useOverlays is an app's held health by backup name.
export function useOverlays(appId: string): Readonly<Record<string, Overlay>> {
  return useSyncExternalStore(
    overlayStore.subscribe,
    () => overlayStore.get()[appId] ?? NO_OVERLAYS,
    () => NO_OVERLAYS,
  );
}

// resetHeldHealth forgets every held health (tests share this module's state).
export function resetHeldHealth() {
  overlayStore.set({});
}
