import { createContext } from "react";

export type App = {
  id: string;
  serverId: string;
  name: string;
  version: string;
  icon: string;
  color: string;
  createdAt: string;
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

export type AppsContextValue = {
  apps: App[];
  // statusByApp maps app id -> latest container status code (live, in-memory).
  statusByApp: Record<string, number>;
  loading: boolean;
  error: string | null;
  refresh: () => Promise<void>;
  // Lifecycle actions: dispatch the command, await its SSE result, then refresh.
  control: (appId: string, action: ControlAction) => Promise<void>;
  remove: (appId: string) => Promise<void>;
  rename: (appId: string, name: string) => Promise<void>;
  // createApp dispatches app.save with the full payload (config + files +
  // variables) and awaits the result.
  createApp: (body: {
    draft?: boolean;
    source?: unknown;
    app: Record<string, unknown>;
    config: unknown;
    files: { name: string; content: string; encrypted: boolean }[];
    variables: { name: string; content: string; encrypted: boolean }[];
  }) => Promise<void>;
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
  revision: number;
  available_revisions: number[] | null;
};

export const AppsContext = createContext<AppsContextValue | undefined>(
  undefined,
);
