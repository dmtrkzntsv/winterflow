import { useEffect, useMemo, useRef, useState, useSyncExternalStore } from "react";
import { useNavigate } from "react-router-dom";
import { toast } from "sonner";

import { SaveFailedError, type App } from "@/context/apps-context-base";
import { WaitTimeoutError } from "@/context/notifications-context-base";
import { useApps } from "@/context/use-apps";
import { buildSavePayload } from "@/lib/app-editor-io";
import { errorHeadline } from "@/lib/errors";
import {
  claimCreate,
  createSlot,
  heldCreates,
  holdCreate,
  onLateCreate,
  releaseCreate,
  subscribeHeldCreates,
  type HeldCreate,
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

// useCreateApp saves a new app (app.save without an id: the agent creates
// and deploys it) and opens its Overview, or the Apps page when the answer
// doesn't list it yet. A failed first deploy may leave the app behind (the
// agent commits it before compose up): the agent's list is read again, and
// when it holds the new app its page opens with the failure noted (it reads
// Failed and shows why). A create that left no app stays on the form as
// error until the next attempt (or dismiss).
//
// A create the agent hasn't answered in time is still running: its name
// stays claimed (lib/held-creates), even after the form is left, so it
// can't be created twice; stillCreating names the newest such create on
// this server. When its app appears in the list, a form that sent it opens
// it; when its late result says it failed, the form shows why (or opens the
// app it left behind, failure noted) and Create is free again.
export function useCreateApp(serverId: string) {
  const { apps, saveApp, getPublicKey, reconcile, noteDeployFailure } = useApps();
  const navigate = useNavigate();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const all = useSyncExternalStore(subscribeHeldCreates, heldCreates, heldCreates);
  const held = useMemo(() => all.filter((h) => h.serverId === serverId), [all, serverId]);
  // mine are the slots this form sent that outlived their wait: only those
  // open their app when it appears. landing are the ones whose late result
  // said they went through (their slot is free already), until the list
  // shows the app.
  const mine = useRef(new Set<string>());
  const landing = useRef<HeldCreate[]>([]);

  // A slow create appeared (the list is re-read when its answer comes, and
  // polled): its name is free again; the newest one this form sent opens.
  useEffect(() => {
    const appeared = held.filter((h) => createdApp(apps, h.before, h.name));
    for (const h of appeared) releaseCreate(h.slot);
    const landed = landing.current.filter((h) => createdApp(apps, h.before, h.name));
    landing.current = landing.current.filter((h) => !landed.includes(h));
    const own = [...appeared.filter((h) => mine.current.has(h.slot)), ...landed];
    for (const h of own) mine.current.delete(h.slot);
    const newest = own.at(-1);
    if (!newest) return;
    toast.success("App created");
    navigate(appPath(serverId, createdApp(apps, newest.before, newest.name)!.id));
  }, [apps, held, navigate, serverId]);

  // A held create's late result said it failed: Create is free again (the
  // slot went with the result); the app it may have left behind opens with
  // the failure noted, else the form says why.
  useEffect(
    () =>
      onLateCreate((create, outcome) => {
        if (create.serverId !== serverId || !mine.current.has(create.slot)) return;
        if (!outcome.failed) {
          // The list is read again next: the appearing app opens.
          landing.current = [...landing.current, create];
          return;
        }
        mine.current.delete(create.slot);
        void reconcile()
          .catch(() => [] as App[])
          .then((list) => {
            const left = createdApp(list, create.before, create.name);
            if (!left) {
              setError(outcome.error);
              return;
            }
            noteDeployFailure(left.id, outcome.error);
            toast.error("The app was created but couldn't start", { description: errorHeadline(outcome.error) });
            navigate(appPath(serverId, left.id));
          });
      }),
    [serverId, reconcile, noteDeployFailure, navigate],
  );

  const create = async (state: AppEditorState) => {
    const name = state.config.name.trim();
    const slot = createSlot(serverId, name);
    if (!claimCreate(slot)) return;
    const before = new Set(apps.map((a) => a.id));
    let keep = false;
    setBusy(true);
    setError("");
    try {
      const payload = await buildSavePayload(state, getPublicKey);
      let list: App[];
      try {
        list = await saveApp(payload, { timeoutMs: state.config.source ? GIT_CREATE_WAIT_MS : COMPOSE_CREATE_WAIT_MS });
      } catch (e) {
        if (e instanceof WaitTimeoutError) {
          keep = true;
          mine.current.add(slot);
          holdCreate({ slot, serverId, name, before, ref: e.ref });
          return;
        }
        // A refusal (or a failure before the agent ran the save) created
        // nothing; a failed save may have.
        if (!(e instanceof SaveFailedError)) throw e;
        const left = createdApp(await reconcile().catch(() => []), before, name);
        if (!left) throw e;
        noteDeployFailure(left.id, e.message);
        toast.error("The app was created but couldn't start", { description: errorHeadline(e.message) });
        navigate(appPath(serverId, left.id));
        return;
      }
      toast.success("App created and deploying");
      const created = createdApp(list, before, name);
      navigate(created ? appPath(serverId, created.id) : appsPath(serverId));
    } catch (e) {
      setError(e instanceof Error && e.message ? e.message : "Couldn't create the app");
    } finally {
      if (!keep) releaseCreate(slot);
      setBusy(false);
    }
  };

  return {
    create,
    busy,
    error,
    stillCreating: held.at(-1)?.name ?? "",
    // heldNames are the names whose create is still running on this server.
    heldNames: held.map((h) => h.name),
    dismiss: () => setError(""),
  };
}
