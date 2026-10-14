import { useEffect, useMemo, useRef, useState, useSyncExternalStore } from "react";
import { useNavigate } from "react-router-dom";
import { toast } from "sonner";

import { SaveFailedError, type App } from "@/context/apps-context-base";
import { WaitTimeoutError } from "@/context/notifications-context-base";
import { useApps } from "@/context/use-apps";
import { buildSavePayload } from "@/lib/app-editor-io";
import { errorHeadline } from "@/lib/errors";
import {
  attendCreates,
  claimCreate,
  deliverCreate,
  holdCreate,
  pendingCreates,
  releaseCreate,
  subscribePendingCreates,
  type CreateOutcome,
  type PendingCreate,
} from "@/lib/held-creates";
import { createdApp } from "@/lib/new-app";
import { appPath, appsPath } from "@/lib/routes";
import type { AppEditorState } from "@/types/app-config";

// How long a create waits for the agent's answer: a compose app pulls its
// images first; a Git app also clones its repository (the agent allows
// that 5 minutes) before the pulls — below the 10 minutes the API keeps a
// pending command, so its result can still reach the browser.
export const COMPOSE_CREATE_WAIT_MS = 5 * 60_000;
export const GIT_CREATE_WAIT_MS = 9.5 * 60_000;

// leftBehind is the app a failed create left (the agent commits it before
// compose up), from the agent's list read again; null when it left none or
// the list can't be read.
export async function leftBehind(reconcile: () => Promise<App[]>, create: PendingCreate): Promise<App | null> {
  return createdApp(await reconcile().catch(() => [] as App[]), create.before, create.name) ?? null;
}

// landedApp is a create's app in the list read after it went through (the
// active server's: another server's create is never found in it).
function landedApp(create: PendingCreate, outcome: CreateOutcome): App | undefined {
  const list = (outcome.list ?? []).filter((a) => a.serverId === create.serverId);
  return createdApp(list, create.before, create.name);
}

// announceCreate tells, in a toast, how a create ended whose form is gone
// (or now holds another app): ready, with Open; couldn't start, with Open
// to the app it left (its failure noted by the caller); couldn't be
// created.
export function announceCreate(create: PendingCreate, outcome: CreateOutcome, open: (path: string) => void): void {
  if (!outcome.failed) {
    const app = landedApp(create, outcome);
    const to = app ? appPath(create.serverId, app.id) : appsPath(create.serverId);
    toast.success(`${create.name} is ready`, { action: { label: "Open", onClick: () => open(to) } });
    return;
  }
  const description = errorHeadline(outcome.error);
  const left = outcome.left;
  if (left) {
    toast.error(`${create.name} couldn't start`, {
      description,
      action: { label: "Open", onClick: () => open(appPath(create.serverId, left.id)) },
    });
  } else {
    toast.error(`${create.name} couldn't be created`, { description });
  }
}

// useCreateApp saves a new app (app.save without an id: the agent creates
// and deploys it) and opens its Overview, or the Apps page when the answer
// doesn't list it yet. A failed first deploy may leave the app behind (the
// agent commits it before compose up): the agent's list is read again, and
// when it holds the new app its page opens with the failure noted (it reads
// Failed and shows why). A create that left no app stays on the form as
// error until the next attempt (or dismiss). name is the name the form
// holds now: the form is waiting on a create of that name.
//
// A create the agent hasn't answered in time is still running: its name
// stays claimed (lib/held-creates), even after the form is left, so it
// can't be created twice; stillCreating names the newest create on this
// server this form didn't send (or that outlived its wait). How a create
// ends reaches the form open for its server, whichever sent it: when the
// form is waiting on it, its app opens (or it says why it failed);
// otherwise, and when no form is open, a toast says so (CreateOutcomes).
export function useCreateApp(serverId: string, name = "") {
  const { apps, saveApp, getPublicKey, reconcile, noteDeployFailure } = useApps();
  const navigate = useNavigate();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const all = useSyncExternalStore(subscribePendingCreates, pendingCreates, pendingCreates);
  // sending are the slots this form is waiting on (sent, not yet answered
  // or out of time): the form shows those as busy, not as still creating.
  const [sending, setSending] = useState<ReadonlySet<string>>(new Set());
  const held = useMemo(
    () => all.filter((c) => c.serverId === serverId && (c.late || c.remote || !sending.has(c.slot))),
    [all, serverId, sending],
  );
  // live: the form is open; current: the name it holds.
  const live = useRef(true);
  const current = useRef(name);
  current.current = name;
  useEffect(() => {
    live.current = true;
    return () => {
      live.current = false;
    };
  }, []);
  const waitingOn = (c: PendingCreate) => live.current && (!current.current || current.current === c.name);

  // explain ends a failed create on this form: the app it left opens (its
  // failure noted), else the form says why; when the form is gone (or
  // holds another app), a toast does.
  const explain = useRef<(c: PendingCreate, outcome: CreateOutcome) => Promise<void>>(async () => {});
  explain.current = async (c, outcome) => {
    const left = outcome.left !== undefined ? outcome.left : await leftBehind(reconcile, c);
    if (left) noteDeployFailure(left.id, outcome.error);
    if (!waitingOn(c)) {
      deliverOrAnnounce(c, { ...outcome, left });
      return;
    }
    if (!left) {
      setError(outcome.error);
      return;
    }
    toast.error("The app was created but couldn't start", { description: errorHeadline(outcome.error) });
    navigate(appPath(serverId, left.id));
  };
  // deliverOrAnnounce passes an outcome on: to another form open for the
  // server when this one is gone, else in a toast.
  const deliverOrAnnounce = (c: PendingCreate, outcome: CreateOutcome) => {
    if (live.current) announceCreate(c, outcome, navigate);
    else deliverCreate(c, outcome);
  };

  // How a create ended whose own form is gone (or a late one).
  useEffect(
    () =>
      attendCreates(serverId, (c, outcome) => {
        if (outcome.failed) {
          void explain.current(c, outcome);
          return;
        }
        if (!waitingOn(c)) {
          announceCreate(c, outcome, navigate);
          return;
        }
        const app = landedApp(c, outcome);
        toast.success("App created");
        navigate(app ? appPath(serverId, app.id) : appsPath(serverId));
      }),
    // waitingOn and explain read refs only.
    [serverId, navigate],
  );

  // A slow create appeared (the list is re-read when its answer comes):
  // its name is free again, and it opens (the newest, when several did).
  useEffect(() => {
    const appeared = held.filter((c) => c.late && !c.remote && createdApp(apps, c.before, c.name));
    const mine = appeared.filter((c) => releaseCreate(c.slot));
    const newest = mine.filter(waitingOn).at(-1);
    for (const c of mine) {
      if (c !== newest) announceCreate(c, { failed: false, error: "", list: apps }, navigate);
    }
    if (!newest) return;
    toast.success("App created");
    navigate(appPath(serverId, createdApp(apps, newest.before, newest.name)!.id));
  }, [apps, held, navigate, serverId]);

  const create = async (state: AppEditorState) => {
    const name = state.config.name.trim();
    const c = claimCreate(serverId, name, new Set(apps.map((a) => a.id)));
    if (!c) return;
    let late = false;
    const settle = (on: boolean) =>
      setSending((prev) => {
        const next = new Set(prev);
        if (on) next.add(c.slot);
        else next.delete(c.slot);
        return next;
      });
    settle(true);
    setBusy(true);
    setError("");
    try {
      const payload = await buildSavePayload(state, getPublicKey);
      const list = await saveApp(payload, { timeoutMs: state.config.source ? GIT_CREATE_WAIT_MS : COMPOSE_CREATE_WAIT_MS });
      releaseCreate(c.slot);
      if (!live.current) {
        deliverCreate(c, { failed: false, error: "", list });
        return;
      }
      toast.success("App created and deploying");
      const created = createdApp(list, c.before, name);
      navigate(created ? appPath(serverId, created.id) : appsPath(serverId));
    } catch (e) {
      if (e instanceof WaitTimeoutError) {
        late = true;
        holdCreate(c.slot, e.ref);
        return;
      }
      releaseCreate(c.slot);
      const message = e instanceof Error && e.message ? e.message : "Couldn't create the app";
      // A refusal (or a failure before the agent ran the save) created
      // nothing; a failed save may have.
      const outcome: CreateOutcome = { failed: true, error: message, ...(e instanceof SaveFailedError ? {} : { left: null }) };
      if (live.current) await explain.current(c, outcome);
      else deliverCreate(c, outcome);
    } finally {
      if (!late) releaseCreate(c.slot);
      settle(false);
      if (live.current) setBusy(false);
    }
  };

  return {
    create,
    busy,
    error,
    stillCreating: held.at(-1)?.name ?? "",
    // heldNames are the names whose create is still running on this server.
    heldNames: held.map((c) => c.name),
    dismiss: () => setError(""),
  };
}
