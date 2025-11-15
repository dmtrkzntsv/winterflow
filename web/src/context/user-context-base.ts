import { createContext } from "react";

export type UserProfile = {
  id: string;
  name: string;
  picture: string;
  provider?: string;
};

export type UserContextValue = {
  user: UserProfile | null;
  loading: boolean;
  error: string | null;
  refresh: () => Promise<void>;
};

export const UserContext = createContext<UserContextValue | undefined>(
  undefined,
);
