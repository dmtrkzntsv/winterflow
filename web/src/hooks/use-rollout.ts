import { useCallback, useEffect, useState } from "react";

import { useNotifications } from "@/context/use-notifications";
import { ROLLOUT_EVENT, type Rollout, type RolloutKind, type RolloutsApi } from "@/lib/rollouts";

// useRollout follows the newest rollout of a kind (of one scope, when
// given; null: none yet, follow nothing) for a page: read once, then kept
// current by the stream's rollout events. set shows one this page just
// queued; stop asks the API to stop it and shows the answer. A failed read
// leaves no rollout (the page works without it).
export function useRollout(api: RolloutsApi, kind: RolloutKind, scopeKey?: string | null) {
  const { subscribe } = useNotifications();
  // The state is keyed to what it follows: a change of kind or scope shows
  // nothing until the new one's read or events arrive.
  const key = `${kind}:${scopeKey ?? ""}:${scopeKey === null ? "none" : "scope"}`;
  const [state, setState] = useState<{ key: string; rollout: Rollout | null }>({ key, rollout: null });
  const rollout = state.key === key ? state.rollout : null;

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

  useEffect(
    () =>
      subscribe((n) => {
        if (n.type !== ROLLOUT_EVENT || scopeKey === null) return;
        const r = n.payload as Rollout | undefined;
        if (!r || r.kind !== kind || (scopeKey && r.scope_key !== scopeKey)) return;
        // A newer rollout replaces the shown one; an older one's late event
        // does not.
        update(key, (cur) => (!cur || cur.id === r.id || r.created_at >= cur.created_at ? r : cur));
      }),
    [subscribe, kind, scopeKey, key, update],
  );

  const set = useCallback((r: Rollout | null) => update(key, () => r), [key, update]);

  const stop = useCallback(
    async (id: string) => {
      set(await api.stop(id));
    },
    [api, set],
  );

  return { rollout, set, stop };
}
