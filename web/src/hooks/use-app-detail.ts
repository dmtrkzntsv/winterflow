import { useCallback, useEffect, useRef, useState } from "react";

import type { AppDetailPayload } from "@/context/apps-context-base";
import { useApps } from "@/context/use-apps";

// shared is the latest read of the app open in the page, with the version
// it was asked for: the page's read and Configuration's of one version are
// one read (each is a command to the agent). Only one app's is kept (a
// read of another app drops it), and only while its page is open.
const shared = new Map<string, { version: string; read: Promise<AppDetailPayload> }>();

// readAppDetail reads an app's version once: a read of the same version
// already asked for (or under way) is shared, unless again asks for a new
// one (which the next shared read then gets). A failed read is not kept.
// An app without a version (the API stores none after a save) can't be told
// apart from its next save: every read of it is fresh and none is kept.
export function readAppDetail(
  getApp: (appId: string) => Promise<AppDetailPayload>,
  appId: string,
  version: string,
  again = false,
): Promise<AppDetailPayload> {
  if (!version) {
    shared.delete(appId);
    return getApp(appId);
  }
  const hit = shared.get(appId);
  if (!again && hit && hit.version === version) return hit.read;
  return keep(appId, version, getApp(appId));
}

function keep(appId: string, version: string, read: Promise<AppDetailPayload>): Promise<AppDetailPayload> {
  shared.clear();
  shared.set(appId, { version, read });
  read.catch(() => {
    if (shared.get(appId)?.read === read) shared.delete(appId);
  });
  return read;
}

// seedAppDetail shares a read made elsewhere (Configuration's fresh read
// after a save) as the read of that version; nothing for an app without a
// version.
export function seedAppDetail(appId: string, version: string | undefined, detail: AppDetailPayload): void {
  if (version) keep(appId, version, Promise.resolve(detail));
}

// forgetAppDetail drops an app's shared read (a save changed the app: the
// version it was kept under may not move).
export function forgetAppDetail(appId: string): void {
  shared.delete(appId);
}

// forgetAppDetails drops every shared read (tests).
export function forgetAppDetails() {
  shared.clear();
}

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

// useAppDetail reads the app once per app and version (shared with
// Configuration's read of it, see readAppDetail), and again, afresh, per
// revisit (the caller bumps it: the Overview shown again) and per reload.
export function useAppDetail(appId: string, version: string, revisit = 0): AppDetailRead {
  const { getApp } = useApps();
  const [attempt, setAttempt] = useState(0);
  const [result, setResult] = useState<Result | null>(null);
  const key = `${version}:${revisit}`;
  // last is the read this hook asked for last: another read of the same
  // app version (a revisit or reload) is a re-read; the same read asked for
  // again (an effect run twice) is not.
  const last = useRef({ app: "", read: "" });

  useEffect(() => {
    if (!appId) return;
    let live = true;
    const app = `${appId}\n${version}`;
    const read = `${app}\n${revisit}\n${attempt}`;
    const again = last.current.app === app && last.current.read !== read;
    last.current = { app, read };
    readAppDetail(getApp, appId, version, again).then(
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
  }, [appId, version, revisit, key, attempt, getApp]);

  // Leaving the app drops its shared read: the next visit reads afresh.
  useEffect(
    () => () => {
      shared.delete(appId);
    },
    [appId],
  );

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
