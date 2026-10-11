import { createContext } from "react";

import type { BackupHealth, BackupTargets, EffectiveBackup } from "@/lib/backups";
import type { StorageVariable } from "@/types/app-config";

export type AppDomain = {
  domain: string;
  ssl: boolean;
  kind: "route" | "redirect";
};

export type App = {
  id: string;
  serverId: string;
  name: string;
  version: string;
  icon: string;
  color: string;
  createdAt: string;
  domains?: AppDomain[];
};

export type AppRevision = {
  hash: string;
  subject: string;
  timestamp: number; // unix seconds
};

export type AppRevisions = {
  current: string;
  deployed: string; // "" = unknown / never recorded
  revisions: AppRevision[];
};

export type ControlAction = "start" | "stop" | "restart" | "update";

// Container status codes mirror command.ContainerStatusCode on the backend.
export const AppStatusCode = {
  Unknown: 0,
  Active: 1,
  Idle: 2,
  Restarting: 3,
  Problematic: 4,
  Stopped: 5,
} as const;

export type AppStatus = {
  appId: string;
  statusCode: number;
};

export type DeleteAppResult = {
  warnings: string[];
  volumesDeleted: boolean;
  backupsDeleted: boolean;
};

export type AppsContextValue = {
  apps: App[];
  // statusByApp maps app id -> latest container status code (live, in-memory).
  statusByApp: Record<string, number>;
  // backupsByApp maps app id -> the health of each of its backups (live,
  // with the status).
  backupsByApp: Record<string, BackupHealth[]>;
  loading: boolean;
  error: string | null;
  refresh: () => Promise<void>;
  // Lifecycle actions: dispatch the command, await its SSE result, then refresh.
  control: (appId: string, action: ControlAction) => Promise<void>;
  // remove resolves with cleanup warnings (e.g. a volume still in use) and
  // whether the agent honoured deleteVolumes and deleteBackups — an older
  // agent ignores the flags, so they stay false and the caller must not
  // claim success.
  remove: (
    appId: string,
    opts?: { deleteVolumes?: boolean; deleteBackups?: boolean },
  ) => Promise<DeleteAppResult>;
  rename: (appId: string, name: string) => Promise<void>;
  // saveApp dispatches app.save with the full payload (config + files +
  // variables) and awaits the result. Upsert: app.id present = edit,
  // absent = create. Resolves with the refreshed app list so callers can
  // locate a freshly created app (e.g. to navigate to it).
  saveApp: (body: {
    draft?: boolean;
    source?: unknown;
    app: Record<string, unknown>;
    config: unknown;
    files: { name: string; content: string; encrypted: boolean }[];
    variables: { name: string; content: string; encrypted: boolean }[];
  }) => Promise<App[]>;
  // getPublicKey returns the server's ECIES public key for encrypting secrets.
  getPublicKey: () => Promise<string>;
  // getImageTags lists the registry tags available for an image (via the
  // agent's docker credentials).
  getImageTags: (image: string) => Promise<string[]>;
  // getRevisions fetches the app's git history from the agent.
  getRevisions: (appId: string) => Promise<AppRevisions>;
  // rollback restores the given commit as a new revision and redeploys.
  rollback: (appId: string, hash: string) => Promise<void>;
  // getApp fetches an app's config + files + variables from the agent (for
  // editing). Secret values are returned masked by the agent.
  getApp: (appId: string) => Promise<AppDetailPayload>;
};

// AppDetailPayload mirrors the agent's GetAppResponse over SSE. Byte fields
// (config, content) arrive base64-encoded.
export type AppDetailPayload = {
  app: {
    app_id: string;
    config: string; // base64 JSON of the stored config
    variables: { id?: string; name: string; content: string; encrypted?: boolean }[] | null;
    files: { id?: string; name: string; content: string; encrypted?: boolean }[] | null;
  };
  // WF_STORAGE_ variables the compose files reference (never values).
  storage_variables?: StorageVariable[] | null;
  // The app's backups (recipe + its settings), why they can't be read,
  // where a new one can point, and the server's BACKUP_RETENTION_DAYS.
  backups?: EffectiveBackup[] | null;
  backups_error?: string;
  // Identifies the stored backups list: sent back with the next update, so
  // a change made elsewhere meanwhile is refused instead of undone.
  backups_base?: string;
  backup_targets?: BackupTargets | null;
  retention_days?: number;
  revision: number;
  available_revisions: number[] | null;
};

export const AppsContext = createContext<AppsContextValue | undefined>(
  undefined,
);
