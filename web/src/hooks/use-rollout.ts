import { useCallback, useEffect, useState } from "react";

import { useNotifications } from "@/context/use-notifications";
import { ROLLOUT_EVENT, isActive, type Rollout, type RolloutKind, type RolloutsApi } from "@/lib/rollouts";

// REREAD_MS is how often an active rollout is re-read while the page is
// visible: with several API replicas a step's events reach only the
// browsers on the worker's replica, so the others catch up this way.
export const REREAD_MS = 15_000;

// settle is what replaces cur with next: the same rollout never goes from
// ended (done, stopped, failed) back to active — a late stop answer or
// event, or a re-read that crossed the end, is older news.
function settle(cur: Rollout | null, next: Rollout | null): Rollout | null {
  if (cur && next && cur.id === next.id && !isActive(cur) && isActive(next)) return cur;
  return next;
}

// fromList is what a read shows: its newest rollout when that is a newer
// one than cur, else cur's fresh copy (or cur, when the read lacks it).
function fromList(cur: Rollout | null, list: Rollout[]): Rollout | null {
  const newest = list[0] ?? null;
  if (!cur) return newest;
  if (newest && newest.id !== cur.id && newest.created_at >= cur.created_at) return newest;
  return settle(cur, list.find((r) => r.id === cur.id) ?? cur);
}

// useRollout follows the newest rollout of a kind (of one scope, when
// given; null: none yet, follow nothing) for a page: read once, then kept
// current by the stream's rollout events, and re-read every REREAD_MS
// while the shown rollout is active and the page visible. set shows one
// this page just queued; stop asks the API to stop it and shows the
// answer. A failed read leaves no rollout (the page works without it).
export function useRollout(api: RolloutsApi, kind: RolloutKind, scopeKey?: string | null) {
  const { subscribe } = useNotifications();
  // The state is keyed to what it follows: a change of kind or scope shows
  // nothing until the new one's read or events arrive.
  const key = `${kind}:${scopeKey ?? ""}:${scopeKey === null ? "none" : "scope"}`;
  const [state, setState] = useState<{ key: string; rollout: Rollout | null }>({ key, rollout: null });
  const rollout = state.key === key ? state.rollout : null;
  const active = rollout ? isActive(rollout) : false;

  const update = useCallback(
    (k: string, next: (cur: Rollout | null) => Rollout | null) =>
      setState((s) => ({ key: k, rollout: next(s.key === k ? s.rollout : null) })),
    [],
  );

  useEffect(() => {
    if (scopeKey === null) return;
    let live = true;
    api.list(kind, scopeKey).then(
      // An event that arrived meanwhile is newer than the read.
      (list) => live && update(key, (cur) => cur ?? list[0] ?? null),
      () => {},
    );
    return () => {
      live = false;
    };
  }, [api, kind, scopeKey, key, update]);

  useEffect(() => {
    if (scopeKey === null || !active) return;
    let live = true;
    const timer = window.setInterval(() => {
      if (document.visibilityState !== "visible") return;
      api.list(kind, scopeKey).then(
        (list) => live && update(key, (cur) => fromList(cur, list)),
        () => {},
      );
    }, REREAD_MS);
    return () => {
      live = false;
      window.clearInterval(timer);
    };
  }, [api, kind, scopeKey, key, active, update]);

  useEffect(
    () =>
      subscribe((n) => {
        if (n.type !== ROLLOUT_EVENT || scopeKey === null) return;
        const r = n.payload as Rollout | undefined;
        if (!r || r.kind !== kind || (scopeKey && r.scope_key !== scopeKey)) return;
        // A newer rollout replaces the shown one; an older one's late event
        // does not.
        update(key, (cur) => (!cur || cur.id === r.id || r.created_at >= cur.created_at ? settle(cur, r) : cur));
      }),
    [subscribe, kind, scopeKey, key, update],
  );

  const set = useCallback((r: Rollout | null) => update(key, (cur) => settle(cur, r)), [key, update]);

  const stop = useCallback(
    async (id: string) => {
      set(await api.stop(id));
    },
    [api, set],
  );

  return { rollout, set, stop };
}
