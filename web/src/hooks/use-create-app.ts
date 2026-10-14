import { useState } from "react";
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

// useCreateApp saves a new app (app.save without an id: the agent creates
// and deploys it) and opens its Overview, or the Apps page when the answer
// doesn't list it yet. A failed first deploy may leave the app behind (the
// agent commits it before compose up): the agent's list is read again, and
// when it holds the new app its page opens with the failure noted (it reads
// Failed and shows why). A create that left no app stays on the form as
// error until the next attempt (or dismiss).
export function useCreateApp(serverId: string) {
  const { apps, saveApp, getPublicKey, reconcile, noteDeployFailure } = useApps();
  const navigate = useNavigate();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const create = async (state: AppEditorState) => {
    const name = state.config.name.trim();
    const slot = `${serverId}\n${name}`;
    if (pending.has(slot)) return;
    pending.add(slot);
    const before = new Set(apps.map((a) => a.id));
    setBusy(true);
    setError("");
    try {
      const payload = await buildSavePayload(state, getPublicKey);
      let list: App[];
      try {
        list = await saveApp(payload);
      } catch (e) {
        // A refusal (or a failure before the agent ran the save) created
        // nothing; a failed or unanswered save may have.
        if (!(e instanceof SaveFailedError || e instanceof WaitTimeoutError)) throw e;
        const left = createdApp(await reconcile().catch(() => []), before, name);
        if (!left) throw e;
        if (e instanceof SaveFailedError) {
          noteDeployFailure(left.id, e.message);
          toast.error("The app was created but couldn't start", { description: errorHeadline(e.message) });
        } else {
          toast.info("The app was created. Its first start hasn't answered yet.");
        }
        navigate(appPath(serverId, left.id));
        return;
      }
      toast.success("App created and deploying");
      const created = createdApp(list, before, name);
      navigate(created ? appPath(serverId, created.id) : appsPath(serverId));
    } catch (e) {
      setError(e instanceof Error && e.message ? e.message : "Couldn't create the app");
    } finally {
      pending.delete(slot);
      setBusy(false);
    }
  };

  return { create, busy, error, dismiss: () => setError("") };
}
