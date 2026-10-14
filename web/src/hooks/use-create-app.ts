import { useEffect, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { toast } from "sonner";

import { SaveFailedError, type App } from "@/context/apps-context-base";
import { WaitTimeoutError } from "@/context/notifications-context-base";
import { useApps } from "@/context/use-apps";
import { buildSavePayload } from "@/lib/app-editor-io";
import { errorHeadline } from "@/lib/errors";
import { createdApp } from "@/lib/new-app";
import { appPath, appsPath } from "@/lib/routes";
import type { AppEditorState } from "@/types/app-config";

// pending holds the creates in flight by server and name: a second Create
// of the same app while the first runs is ignored, even from a form that
// was left and opened again meanwhile.
const pending = new Set<string>();

// How long a create waits for the agent's answer: a compose app pulls its
// images first; a Git app also clones its repository (the agent allows
// that 5 minutes) before the pulls.
export const COMPOSE_CREATE_WAIT_MS = 5 * 60_000;
export const GIT_CREATE_WAIT_MS = 10 * 60_000;

type Held = { slot: string; name: string; before: ReadonlySet<string> };

// useCreateApp saves a new app (app.save without an id: the agent creates
// and deploys it) and opens its Overview, or the Apps page when the answer
// doesn't list it yet. A failed first deploy may leave the app behind (the
// agent commits it before compose up): the agent's list is read again, and
// when it holds the new app its page opens with the failure noted (it reads
// Failed and shows why). A create that left no app stays on the form as
// error until the next attempt (or dismiss).
//
// A create the agent hasn't answered in time is still running (on a
// standalone server every other command waits behind it): its name stays
// claimed, so it can't be created twice, and stillCreating names it until
// the app appears in the list (its page then opens) or the form is left.
export function useCreateApp(serverId: string) {
  const { apps, saveApp, getPublicKey, reconcile, noteDeployFailure } = useApps();
  const navigate = useNavigate();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [stillCreating, setStillCreating] = useState("");
  // held are the creates that outlived their wait, newest last.
  const held = useRef<Held[]>([]);

  // Leaving the form gives the names back.
  useEffect(
    () => () => {
      for (const h of held.current) pending.delete(h.slot);
      held.current = [];
    },
    [],
  );

  // A slow create appeared (the list is re-read when its answer comes):
  // its name is free again; the newest one's page opens.
  useEffect(() => {
    if (held.current.length === 0) return;
    const appeared = held.current.filter((h) => createdApp(apps, h.before, h.name));
    if (appeared.length === 0) return;
    for (const h of appeared) pending.delete(h.slot);
    held.current = held.current.filter((h) => !appeared.includes(h));
    const newest = appeared[appeared.length - 1];
    setStillCreating(held.current.at(-1)?.name ?? "");
    toast.success("App created");
    navigate(appPath(serverId, createdApp(apps, newest.before, newest.name)!.id));
  }, [apps, navigate, serverId]);

  const create = async (state: AppEditorState) => {
    const name = state.config.name.trim();
    const slot = `${serverId}\n${name}`;
    if (pending.has(slot)) return;
    pending.add(slot);
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
          held.current = [...held.current, { slot, name, before }];
          setStillCreating(name);
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
      if (!keep) pending.delete(slot);
      setBusy(false);
    }
  };

  return { create, busy, error, stillCreating, dismiss: () => setError("") };
}
