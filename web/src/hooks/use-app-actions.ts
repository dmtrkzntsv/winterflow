import { useCallback, useState } from "react";
import { useNavigate } from "react-router-dom";
import { toast } from "sonner";

import type { DeleteAppResult } from "@/context/apps-context-base";
import { useApps } from "@/context/use-apps";
import { useServerPath } from "@/context/use-server-path";
import { deleteOutcome, type DeleteChoice } from "@/lib/delete-app";
import { appLabel } from "@/lib/kpi";
import { NEW_TEMPLATE_PATH } from "@/lib/routes";
import { templateFromApp, type DraftState } from "@/lib/template-draft";

// useDeleteApp deletes an app and leaves its page for the Apps list,
// telling what was kept (volumes, backups) or why not everything went. A
// failure is toasted and thrown again, so the confirm dialog stays open for
// a retry.
// onGone runs once the app is deleted, before the page is left (e.g. to
// drop an unsaved-edits guard: the edits went with the app).
export function useDeleteApp(appId: string, onGone?: () => void): (choice: DeleteChoice) => Promise<void> {
  const { remove, backupsByApp } = useApps();
  const navigate = useNavigate();
  const toServer = useServerPath();
  return useCallback(
    async (choice: DeleteChoice) => {
      const hadBackups = (backupsByApp?.[appId]?.length ?? 0) > 0;
      let res: DeleteAppResult;
      try {
        res = await remove(appId, { deleteVolumes: choice.withVolumes, deleteBackups: choice.withBackups });
      } catch (e) {
        toast.error("Delete failed", { description: e instanceof Error ? e.message : undefined });
        throw e;
      }
      onGone?.();
      navigate(toServer());
      const out = deleteOutcome(choice, res, { hadBackups });
      if (out.level === "warning") toast.warning(out.title, { description: out.description });
      else toast.success(out.title, { description: out.description });
    },
    [appId, backupsByApp, navigate, remove, toServer, onGone],
  );
}

// useSaveAsTemplate opens a new template's editor prefilled from what the
// app's editor holds (its files, its variables as parameters, its hosts and
// backup settings); nothing is saved until the template is. busy while the
// app is read.
export function useSaveAsTemplate(appId: string, name: string): { run: () => Promise<void>; busy: boolean } {
  const { getApp } = useApps();
  const navigate = useNavigate();
  const [busy, setBusy] = useState(false);
  const run = useCallback(async () => {
    setBusy(true);
    try {
      const state: DraftState = { ...templateFromApp(appId, await getApp(appId)), appName: appLabel(name) };
      navigate(NEW_TEMPLATE_PATH, { state });
    } catch (e) {
      toast.error("Couldn't read the app", { description: e instanceof Error ? e.message : undefined });
      setBusy(false);
    }
  }, [appId, getApp, name, navigate]);
  return { run, busy };
}
