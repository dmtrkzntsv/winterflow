import { useCallback, useEffect, useRef, useState } from "react";

import type { AppRevisions } from "@/context/apps-context-base";
import { useApps } from "@/context/use-apps";

export type AppRevisionsState = {
  data: AppRevisions | null;
  loading: boolean;
  error: string | null;
  reload: () => Promise<void>;
};

// What reads kept for the session, per app and cache key (see
// useAppRevisions). A history walk is the expensive read here (it grows with
// the app's git history), so a page that is opened again for the same
// version of the app doesn't repeat it.
const kept = new Map<string, { key: string; data: AppRevisions }>();

// resetRevisionsCache forgets every kept history (tests).
export function resetRevisionsCache() {
  kept.clear();
}

// What the last finished read said, and for which app: another app's
// answer is never shown.
type Settled = { appId: string; data: AppRevisions | null; error: string | null };

// useAppRevisions reads an app's history from the agent (one round trip per
// read) while enabled. A read that is no longer the latest (another app, a
// reload) is dropped. With a cacheKey (the app's version) a read is served
// from memory when the same app was read under the same key; reload and
// reads without a key always ask the agent, and replace what was kept, so a
// history that moved on (a deploy, a rollback) is never shown stale.
export function useAppRevisions(appId: string, enabled = true, cacheKey?: string): AppRevisionsState {
  const { getRevisions } = useApps();
  const [settled, setSettled] = useState<Settled | null>(null);
  const [reloading, setReloading] = useState(false);
  const latest = useRef(0);
  // Enabling starts a read: until it lands, whatever an earlier one left is
  // not settled (adjusted during render).
  const [wasEnabled, setWasEnabled] = useState(enabled);
  if (wasEnabled !== enabled) {
    setWasEnabled(enabled);
    if (enabled) setReloading(true);
  }

  // read is the one place the agent is asked, with the cache in front.
  const read = useCallback(
    async (fresh: boolean): Promise<Settled> => {
      const hit = kept.get(appId);
      if (!fresh && cacheKey !== undefined && hit?.key === cacheKey) return { appId, data: hit.data, error: null };
      try {
        const data = await getRevisions(appId);
        if (cacheKey === undefined) kept.delete(appId);
        else kept.set(appId, { key: cacheKey, data });
        return { appId, data, error: null };
      } catch (e) {
        kept.delete(appId);
        return { appId, data: null, error: e instanceof Error ? e.message : "Failed to load history" };
      }
    },
    [appId, cacheKey, getRevisions],
  );

  useEffect(() => {
    if (!enabled) return;
    let live = true;
    const n = ++latest.current;
    void read(false).then((outcome) => {
      if (!live || n !== latest.current) return;
      setSettled(outcome);
      setReloading(false);
    });
    return () => {
      live = false;
    };
  }, [enabled, read]);

  const reload = useCallback(async () => {
    const n = ++latest.current;
    setReloading(true);
    const outcome = await read(true);
    if (n !== latest.current) return;
    setSettled(outcome);
    setReloading(false);
  }, [read]);

  const mine = settled?.appId === appId ? settled : null;
  return {
    data: mine?.data ?? null,
    loading: enabled && (reloading || mine === null),
    error: mine?.error ?? null,
    reload,
  };
}
