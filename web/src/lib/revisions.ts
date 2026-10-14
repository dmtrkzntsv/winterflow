import { useSyncExternalStore } from "react";

import type { AppRevisions } from "@/context/apps-context-base";

// revisionFlags says what a version of an app is: the newest saved one
// (current), the one running now (deployed), or a draft: newest but not
// deployed. An app that was never deployed has no live version and no draft.
export function revisionFlags(
  data: Pick<AppRevisions, "current" | "deployed">,
  hash: string,
): { isCurrent: boolean; isDeployed: boolean; isUndeployedDraft: boolean } {
  const isCurrent = hash === data.current;
  const isDeployed = data.deployed !== "" && hash === data.deployed;
  return { isCurrent, isDeployed, isUndeployedDraft: isCurrent && data.deployed !== "" && !isDeployed };
}

// What reads kept for the session, per app and cache key (see
// useAppRevisions). A history walk is the expensive read here (it grows with
// the app's git history), so a page opened again for the same version of the
// app doesn't repeat it. The version (a commit count) can't see a deploy
// that makes no commit (a plain start brings the draft live), so whatever
// deploys, rolls back or deletes calls forgetRevisions.
const kept = new Map<string, { key: string; data: AppRevisions }>();

// epochs count the forgets per app; a mounted history re-reads when its
// app's changes.
const epochs = new Map<string, number>();
const listeners = new Set<() => void>();

const subscribe = (fn: () => void) => {
  listeners.add(fn);
  return () => void listeners.delete(fn);
};

export function keptRevisions(appId: string, key: string): AppRevisions | null {
  const hit = kept.get(appId);
  return hit?.key === key ? hit.data : null;
}

// keepRevisions stores a read, unless the app was forgotten since it began
// (at `epoch`): then it may already be out of date. A read without a key (the
// History sheet's) refreshes the data of what is kept under its key, and
// keeps nothing new; a failed read drops what was kept.
export function keepRevisions(appId: string, key: string | undefined, data: AppRevisions | null, epoch: number) {
  if (data === null || epoch !== (epochs.get(appId) ?? 0)) kept.delete(appId);
  else if (key !== undefined) kept.set(appId, { key, data });
  else {
    const hit = kept.get(appId);
    if (hit) kept.set(appId, { key: hit.key, data });
  }
}

// forgetRevisions drops an app's kept history and tells mounted histories of
// it to read again. With gone (the app was deleted) its bookkeeping is
// dropped altogether.
export function forgetRevisions(appId: string, opts?: { gone?: boolean }) {
  kept.delete(appId);
  if (opts?.gone) epochs.delete(appId);
  else epochs.set(appId, (epochs.get(appId) ?? 0) + 1);
  for (const fn of [...listeners]) fn();
}

// resetRevisionsCache forgets every app (tests).
export function resetRevisionsCache() {
  kept.clear();
  epochs.clear();
}

export function revisionsEpoch(appId: string): number {
  return epochs.get(appId) ?? 0;
}

// useRevisionsEpoch is the app's forget count, as state.
export function useRevisionsEpoch(appId: string): number {
  return useSyncExternalStore(
    subscribe,
    () => epochs.get(appId) ?? 0,
    () => 0,
  );
}
