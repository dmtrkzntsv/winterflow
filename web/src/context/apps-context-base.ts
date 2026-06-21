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
    app: Record<string, unknown>;
    config: unknown;
    files: { name: string; content: string; encrypted: boolean }[];
    variables: { name: string; content: string; encrypted: boolean }[];
  }) => Promise<void>;
  // getPublicKey returns the server's ECIES public key for encrypting secrets.
  getPublicKey: () => Promise<string>;
};

export const AppsContext = createContext<AppsContextValue | undefined>(
  undefined,
);
