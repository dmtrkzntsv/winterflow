import { useSyncExternalStore } from "react";

// Back up now and Restore answer over SSE when they end, up to ~2 hours
// later. The page must stay usable meanwhile, and switching tabs must not
// forget a run in flight, so what is running lives here, not in a component.

export type Activity = "backup" | "restore";

// What is running, by app id then backup name: every run in flight (a
// Back up now and a restore of one backup can overlap, each ends on its
// own). A new object per change: useSyncExternalStore compares snapshots by
// identity.
type Running = Readonly<Record<string, readonly Activity[]>>;
type ActivityMap = Readonly<Record<string, Running>>;

let snapshot: ActivityMap = {};
const listeners = new Set<() => void>();

function change(appId: string, backup: string, update: (kinds: readonly Activity[]) => readonly Activity[]) {
  const app = { ...snapshot[appId] };
  const kinds = update(app[backup] ?? []);
  if (kinds.length > 0) app[backup] = kinds;
  else delete app[backup];
  const next = { ...snapshot, [appId]: app };
  if (Object.keys(app).length === 0) delete (next as Record<string, unknown>)[appId];
  snapshot = next;
  for (const l of listeners) l();
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

const subscribe = (l: () => void) => {
  listeners.add(l);
  return () => {
    listeners.delete(l);
  };
};

const NONE: Running = {};

// useActivity is what is running for an app: backup name -> its runs.
export function useActivity(appId: string): Running {
  return useSyncExternalStore(
    subscribe,
    () => snapshot[appId] ?? NONE,
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
