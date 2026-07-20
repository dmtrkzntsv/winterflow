import { useCallback, useEffect, useMemo, useState, type ReactNode } from "react";

import { apiBaseUrl } from "@/config";
import { useAuth } from "@/context/use-auth";
import { ProfileContext, type Profile } from "./profile-context-base";

const profileEndpoint = `${apiBaseUrl}/api/v1/user/get-profile`;

export function ProfileProvider({ children }: { children: ReactNode }) {
  const { isAuthenticated } = useAuth();
  const [profile, setProfile] = useState<Profile | null>(null);
  const [loading, setLoading] = useState(true);

  const refresh = useCallback(async () => {
    if (!isAuthenticated) {
      setProfile(null);
      setLoading(false);
      return;
    }
    setLoading(true);
    try {
      const res = await fetch(profileEndpoint, { credentials: "include" });
      const body = await res.json().catch(() => null);
      if (res.ok && body?.success) {
        setProfile(body.data as Profile);
      }
    } catch (e) {
      console.error("Failed to load profile", e);
    } finally {
      setLoading(false);
    }
  }, [isAuthenticated]);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  const value = useMemo(
    () => ({
      profile,
      loading,
      refresh,
      isAdmin: profile?.role === "owner" || profile?.role === "admin",
    }),
    [profile, loading, refresh],
  );

  return (
    <ProfileContext.Provider value={value}>{children}</ProfileContext.Provider>
  );
}
