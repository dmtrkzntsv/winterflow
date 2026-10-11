import { useSyncExternalStore } from "react";

// Back up now and Restore answer over SSE when they end, up to ~2 hours
// later. The page must stay usable meanwhile, and switching tabs must not
// forget a run in flight, so what is running lives here, not in a component.

export type Activity = "backup" | "restore";

// What is running, by app id then backup name. A new object per change:
// useSyncExternalStore compares snapshots by identity.
export type ActivityMap = Readonly<Record<string, Readonly<Record<string, Activity>>>>;

let snapshot: ActivityMap = {};
const listeners = new Set<() => void>();

function set(appId: string, backup: string, kind: Activity | null) {
  const app = { ...snapshot[appId] };
  if (kind) app[backup] = kind;
  else delete app[backup];
  const next = { ...snapshot, [appId]: app };
  if (Object.keys(app).length === 0) delete (next as Record<string, unknown>)[appId];
  snapshot = next;
  for (const l of listeners) l();
}

// trackActivity marks a backup as running and returns the function that
// clears the mark (call it when the result arrives, success or not).
export function trackActivity(appId: string, backup: string, kind: Activity): () => void {
  set(appId, backup, kind);
  return () => set(appId, backup, null);
}

const subscribe = (l: () => void) => {
  listeners.add(l);
  return () => {
    listeners.delete(l);
  };
};

const NONE: Readonly<Record<string, Activity>> = {};
const empty = () => NONE;

// useActivity is what is running for an app: backup name -> kind.
export function useActivity(appId: string): Readonly<Record<string, Activity>> {
  return useSyncExternalStore(subscribe, () => snapshot[appId] ?? NONE, empty);
}
