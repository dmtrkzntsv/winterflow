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
  const [rollout, setRollout] = useState<Rollout | null>(null);

  useEffect(() => {
    if (scopeKey === null) return;
    let live = true;
    api.list(kind, scopeKey).then(
      // An event that arrived meanwhile is newer than the read.
      (list) => live && setRollout((cur) => cur ?? list[0] ?? null),
      () => {},
    );
    return () => {
      live = false;
    };
  }, [api, kind, scopeKey]);

  useEffect(
    () =>
      subscribe((n) => {
        if (n.type !== ROLLOUT_EVENT || scopeKey === null) return;
        const r = n.payload as Rollout | undefined;
        if (!r || r.kind !== kind || (scopeKey && r.scope_key !== scopeKey)) return;
        // A newer rollout replaces the shown one; an older one's late event
        // does not.
        setRollout((cur) => (!cur || cur.id === r.id || r.created_at >= cur.created_at ? r : cur));
      }),
    [subscribe, kind, scopeKey],
  );

  const stop = useCallback(
    async (id: string) => {
      setRollout(await api.stop(id));
    },
    [api],
  );

  return { rollout, set: setRollout, stop };
}
