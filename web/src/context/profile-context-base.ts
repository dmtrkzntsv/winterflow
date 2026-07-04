import { createContext } from "react";

// Profile is the authenticated user's identity as the API reports it:
// role drives admin-only navigation; email + must_change_password exist
// only for accounts with local credentials.
export type Profile = {
  user_id: string;
  name: string;
  email: string;
  role: string;
  must_change_password: boolean;
};

export type ProfileContextValue = {
  profile: Profile | null;
  loading: boolean;
  refresh: () => Promise<void>;
  isAdmin: boolean;
};

export const ProfileContext = createContext<ProfileContextValue | null>(null);
