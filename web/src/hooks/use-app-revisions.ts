import { useCallback, useEffect, useRef, useState } from "react";

import type { AppRevisions } from "@/context/apps-context-base";
import { useApps } from "@/context/use-apps";

export type AppRevisionsState = {
  data: AppRevisions | null;
  loading: boolean;
  error: string | null;
  reload: () => Promise<void>;
};

// What the last finished read said, and for which app: another app's
// answer is never shown.
type Settled = { appId: string; data: AppRevisions | null; error: string | null };

// useAppRevisions reads an app's history from the agent (one round trip per
// read) while enabled. A read that is no longer the latest (another app, a
// reload) is dropped.
export function useAppRevisions(appId: string, enabled = true): AppRevisionsState {
  const { getRevisions } = useApps();
  const [settled, setSettled] = useState<Settled | null>(null);
  const [reloading, setReloading] = useState(false);
  const latest = useRef(0);

  // read is the one place the agent is asked: a cache keyed by
  // (appId, version) can sit in front of getRevisions here.
  const read = useCallback(async (): Promise<Settled> => {
    try {
      return { appId, data: await getRevisions(appId), error: null };
    } catch (e) {
      return { appId, data: null, error: e instanceof Error ? e.message : "Failed to load history" };
    }
  }, [appId, getRevisions]);

  useEffect(() => {
    if (!enabled) return;
    let live = true;
    const n = ++latest.current;
    void read().then((outcome) => {
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
    const outcome = await read();
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
