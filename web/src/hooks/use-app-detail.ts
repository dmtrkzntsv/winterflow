import { useCallback, useEffect, useState } from "react";

import type { AppDetailPayload } from "@/context/apps-context-base";
import { useApps } from "@/context/use-apps";

// AppDetailRead is one read of an app's files, variables and backups
// (get-app, a command to the agent): detail once read, error when it
// failed (the latest read's only), settled once a read ended either way,
// fresh while the read shown is the latest asked for. A re-read keeps the
// last detail until it ends.
export type AppDetailRead = {
  appId: string;
  detail: AppDetailPayload | null;
  error: string;
  settled: boolean;
  fresh: boolean;
  reload: () => void;
};

type Result = { appId: string; key: string; attempt: number; detail: AppDetailPayload | null; error: string };

// useAppDetail reads the app once per app, per key (the caller changes it
// to read again: a new version, the Overview shown again) and per reload.
export function useAppDetail(appId: string, key: string): AppDetailRead {
  const { getApp } = useApps();
  const [attempt, setAttempt] = useState(0);
  const [result, setResult] = useState<Result | null>(null);

  useEffect(() => {
    if (!appId) return;
    let live = true;
    getApp(appId).then(
      (detail) => {
        if (live) setResult({ appId, key, attempt, detail, error: "" });
      },
      (e) => {
        if (live) setResult({ appId, key, attempt, detail: null, error: e instanceof Error ? e.message : "Couldn't read the app" });
      },
    );
    return () => {
      live = false;
    };
  }, [appId, key, attempt, getApp]);

  const reload = useCallback(() => setAttempt((n) => n + 1), []);
  const mine = result?.appId === appId ? result : null;
  const current = mine !== null && mine.key === key && mine.attempt === attempt;
  return {
    appId,
    // A failed re-read keeps nothing: what it would show may be gone.
    detail: mine?.detail ?? null,
    error: current ? mine.error : "",
    settled: mine !== null,
    fresh: current,
    reload,
  };
}
