import { useCallback, useMemo } from "react";

import { useNotifications } from "@/context/use-notifications";
import { useServers } from "@/context/use-servers";
import { dispatchCommand, readApiError } from "@/lib/api";
import {
  AgentError,
  type BackupConfig,
  type EffectiveBackup,
  type Orphan,
  type RestorePoint,
  type RestoreResult,
  type RunBackupResult,
} from "@/lib/backups";
import { apiBase as base } from "@/config";

// How long to wait for a result. A Back up now or a restore answers when it
// ends, after any backup queued ahead of it: the agent bounds the wait at 2
// hours. update-backups may redeploy the app (10 minutes on the agent).
export const BACKUP_RUN_TIMEOUT_MS = 125 * 60_000;
export const BACKUP_UPDATE_TIMEOUT_MS = 11 * 60_000;
// Listing a storage's orphans is bounded at 1 minute on the agent, deleting
// one at 2: wait a little longer than each, so the agent's own answer (its
// error included) is what the user sees.
export const ORPHAN_LIST_TIMEOUT_MS = 70_000;
export const ORPHAN_DELETE_TIMEOUT_MS = 150_000;

// base identifies the stored list now (the next update's base).
export type BackupsUpdated = { backups: EffectiveBackup[]; redeployed: boolean; warnings: string[]; base: string };

// useBackups exposes the backup routes of the active server: an app's
// backups list, its restore points, Back up now, restore, and the backups of
// apps not on the server. Each dispatches an agent command and awaits its
// SSE result. These routes take server_id in the body (POST) or the query
// (GET).
export function useBackups() {
  const { activeServerId: serverId } = useServers();
  const { waitFor } = useNotifications();

  const dispatch = useCallback(
    async <T>(path: string, params: Record<string, unknown>, opts: { post?: boolean; timeoutMs?: number } = {}): Promise<T> => {
      if (!serverId) throw new Error("No active server");
      let url = `${base}${path}`;
      let init: RequestInit = { credentials: "include" };
      if (opts.post) {
        init = {
          ...init,
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ server_id: serverId, ...params }),
        };
      } else {
        const q = new URLSearchParams({ server_id: serverId });
        for (const [k, v] of Object.entries(params)) q.set(k, String(v));
        url += `?${q.toString()}`;
      }
      return (await dispatchCommand(waitFor, url, init, { timeoutMs: opts.timeoutMs, failure: AgentError })) as T;
    },
    [serverId, waitFor],
  );

  // updateBackups replaces the app's backups list as a whole. base is the
  // backups_base this view was loaded with: the agent refuses the update when
  // the stored list has changed since.
  const updateBackups = useCallback(
    async (appId: string, backups: BackupConfig[], base?: string): Promise<BackupsUpdated> => {
      const res = await dispatch<{
        backups?: EffectiveBackup[] | null;
        redeployed?: boolean;
        warnings?: string[] | null;
        backups_base?: string;
      }>(
        "/api/v1/app/update-backups",
        { app_id: appId, backups, ...(base ? { base } : {}) },
        { post: true, timeoutMs: BACKUP_UPDATE_TIMEOUT_MS },
      );
      return { backups: res.backups ?? [], redeployed: res.redeployed ?? false, warnings: res.warnings ?? [], base: res.backups_base ?? "" };
    },
    [dispatch],
  );

  const listRestorePoints = useCallback(
    async (appId: string, backup: string): Promise<RestorePoint[]> => {
      const res = await dispatch<{ points?: RestorePoint[] | null }>("/api/v1/app/list-restore-points", { app_id: appId, backup });
      return res.points ?? [];
    },
    [dispatch],
  );

  // runBackup resolves when the run ends. A backup already queued or running
  // is refused (the error says so).
  const runBackup = useCallback(
    async (appId: string, backup: string): Promise<RunBackupResult> => {
      const res = await dispatch<{ unchanged?: boolean; point?: RestorePoint | null; warnings?: string[] | null }>(
        "/api/v1/app/run-backup",
        { app_id: appId, backup },
        { post: true, timeoutMs: BACKUP_RUN_TIMEOUT_MS },
      );
      return { unchanged: res.unchanged ?? false, point: res.point ?? null, warnings: res.warnings ?? [] };
    },
    [dispatch],
  );

  // restoreBackup resolves when the restore ends.
  const restoreBackup = useCallback(
    async (appId: string, backup: string, point: string): Promise<RestoreResult> => {
      const res = await dispatch<{ restored_point: string; pre_restore_point?: string; warnings?: string[] | null }>(
        "/api/v1/app/restore-backup",
        { app_id: appId, backup, point },
        { post: true, timeoutMs: BACKUP_RUN_TIMEOUT_MS },
      );
      return { restored_point: res.restored_point, pre_restore_point: res.pre_restore_point, warnings: res.warnings ?? [] };
    },
    [dispatch],
  );

  const deleteRestorePoint = useCallback(
    async (appId: string, backup: string, point: string): Promise<void> => {
      await dispatch("/api/v1/app/delete-restore-point", { app_id: appId, backup, point }, { post: true });
    },
    [dispatch],
  );

  const listOrphans = useCallback(
    async (storage: string): Promise<Orphan[]> => {
      const res = await dispatch<{ orphans?: Orphan[] | null }>("/api/v1/storage/list-orphans", { name: storage }, { timeoutMs: ORPHAN_LIST_TIMEOUT_MS });
      return res.orphans ?? [];
    },
    [dispatch],
  );

  const deleteOrphan = useCallback(
    async (storage: string, appId: string): Promise<number> => {
      const res = await dispatch<{ deleted?: number }>("/api/v1/storage/delete-orphan", { name: storage, app_id: appId }, { post: true, timeoutMs: ORPHAN_DELETE_TIMEOUT_MS });
      return res.deleted ?? 0;
    },
    [dispatch],
  );

  // serverStorages reads the server's storage names from the API's index
  // (no agent round-trip): the storage picker's choices.
  const serverStorages = useCallback(async (): Promise<string[]> => {
    if (!serverId) throw new Error("No active server");
    const res = await fetch(`${base}/api/v1/storage/get-storages?server_id=${encodeURIComponent(serverId)}`, {
      credentials: "include",
    });
    if (!res.ok) throw await readApiError(res);
    const body = (await res.json()) as { data?: { storages?: { name: string }[] | null } };
    return (body.data?.storages ?? []).map((s) => s.name);
  }, [serverId]);

  return useMemo(
    () => ({
      updateBackups,
      listRestorePoints,
      runBackup,
      restoreBackup,
      deleteRestorePoint,
      listOrphans,
      deleteOrphan,
      serverStorages,
    }),
    [updateBackups, listRestorePoints, runBackup, restoreBackup, deleteRestorePoint, listOrphans, deleteOrphan, serverStorages],
  );
}

export type Backups = ReturnType<typeof useBackups>;
