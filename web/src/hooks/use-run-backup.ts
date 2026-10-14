import { useCallback } from "react";
import { toast } from "sonner";

import { useApps } from "@/context/use-apps";
import { useBackups } from "@/context/use-backups";
import { holdHealth, shownHealth, trackActivity, useActivity, useOverlays } from "@/lib/backup-activity";
import { AgentError, isRunRefusal, runnableBackups, type EffectiveBackup } from "@/lib/backups";

// useRunBackup is Back up now for one backup of an app. The run is marked
// as running in the shared activity store (it outlives the tab that started
// it), and its outcome is held as the backup's health until an agent report
// shows it. It toasts the result and never throws.
export function useRunBackup(appId: string): (name: string) => Promise<void> {
  const { backupsByApp } = useApps();
  const { runBackup } = useBackups();
  const overlays = useOverlays(appId);
  const reported = backupsByApp?.[appId];
  return useCallback(
    async (name: string) => {
      const end = trackActivity(appId, name, "backup");
      const prior = shownHealth(
        overlays[name],
        reported?.find((h) => h.name === name),
      );
      try {
        const res = await runBackup(appId, name);
        holdHealth(appId, name, { kind: "backed-up", unchanged: res.unchanged }, prior);
        const title = res.unchanged ? `${name}: nothing changed since the last copy` : `${name} backed up`;
        toast.success(title, { description: res.warnings.join("\n") || undefined });
      } catch (e) {
        const message = e instanceof Error ? e.message : "";
        // The run itself failed (not refused before or while another runs):
        // the row says so now, not when the next report comes.
        if (e instanceof AgentError && message && !isRunRefusal(message)) {
          holdHealth(appId, name, { kind: "failed", error: message }, prior);
        }
        toast.error(`Couldn't back up ${name}`, { description: message || undefined });
      } finally {
        end();
      }
    },
    [appId, runBackup, overlays, reported],
  );
}

// useBackUpNow is the app-wide Back up now (the page's ⋯ menu, ⌘K): every
// backup winterflow can run for the app, at once (the agent runs them one
// after the other). With none set up it says so and sends nothing. busy
// while any backup of the app runs or is being restored, so a second click
// never queues a duplicate.
export function useBackUpNow(appId: string): { run: () => Promise<void>; busy: boolean } {
  const { getApp } = useApps();
  const runOne = useRunBackup(appId);
  const activity = useActivity(appId);
  const busy = Object.keys(activity).length > 0;
  const run = useCallback(async () => {
    let list: EffectiveBackup[];
    try {
      list = (await getApp(appId)).backups ?? [];
    } catch (e) {
      toast.error("Couldn't read the app's backups", { description: e instanceof Error ? e.message : undefined });
      return;
    }
    const ready = runnableBackups(list);
    if (ready.length === 0) {
      toast.info("Nothing to back up yet", { description: "Set up a backup on the Backups tab first." });
      return;
    }
    await Promise.all(ready.map((b) => runOne(b.name)));
  }, [appId, getApp, runOne]);
  return { run, busy };
}
