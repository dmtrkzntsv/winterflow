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

export type AppsContextValue = {
  apps: App[];
  loading: boolean;
  error: string | null;
  refresh: () => Promise<void>;
};

export const AppsContext = createContext<AppsContextValue | undefined>(
  undefined,
);
