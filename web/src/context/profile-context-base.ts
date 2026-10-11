import { createContext } from "react";

// Profile is the authenticated user's identity as the API reports it:
// email + must_change_password exist only for accounts with local
// credentials. There are no roles: every user may do everything.
export type Profile = {
  user_id: string;
  name: string;
  email: string;
  must_change_password: boolean;
};

export type ProfileContextValue = {
  profile: Profile | null;
  loading: boolean;
  refresh: () => Promise<void>;
};

export const ProfileContext = createContext<ProfileContextValue | null>(null);
