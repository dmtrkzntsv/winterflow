import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { toast } from "sonner";

import { useApps } from "@/context/use-apps";
import { buildSavePayload } from "@/lib/app-editor-io";
import { createdApp } from "@/lib/new-app";
import { appPath, appsPath } from "@/lib/routes";
import type { AppEditorState } from "@/types/app-config";

// useCreateApp saves a new app (app.save without an id: the agent creates
// and deploys it) and opens its Overview, or the Apps page when the answer
// doesn't list it yet. A failure stays on the form as error.
export function useCreateApp(serverId: string) {
  const { apps, saveApp, getPublicKey } = useApps();
  const navigate = useNavigate();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const create = async (state: AppEditorState) => {
    const before = new Set(apps.map((a) => a.id));
    setBusy(true);
    setError("");
    try {
      const payload = await buildSavePayload(state, getPublicKey);
      const list = await saveApp(payload);
      toast.success("App created and deploying");
      const created = createdApp(list, before, state.config.name.trim());
      navigate(created ? appPath(serverId, created.id) : appsPath(serverId));
    } catch (e) {
      setError(e instanceof Error && e.message ? e.message : "Couldn't create the app");
    } finally {
      setBusy(false);
    }
  };

  return { create, busy, error };
}
