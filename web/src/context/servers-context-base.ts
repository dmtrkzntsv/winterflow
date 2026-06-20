import { createContext } from "react";

export type Server = {
  id: string;
  organizationId: string;
  name: string;
  createdAt: string;
  lastSeenAt: string | null;
};

export type ServersContextValue = {
  servers: Server[];
  activeServer: Server | null;
  activeServerId: string | null;
  setActiveServerId: (id: string) => void;
  loading: boolean;
  error: string | null;
  refresh: () => Promise<void>;
};

export const ServersContext = createContext<ServersContextValue | undefined>(
  undefined,
);
