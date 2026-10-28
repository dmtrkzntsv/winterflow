import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";

import { apiBase } from "@/config";
import { useAuth } from "@/context/use-auth";
import { apiFetch, readApiError } from "@/lib/api";

import { UserContext } from "./user-context-base";
import type { UserProfile } from "./user-context-base";

const userEndpoint = `${apiBase}/api/v1/user/get-profile`;

// UserProvider is who is signed in, for the account menu.
export function UserProvider({ children }: { children: ReactNode }) {
  const { isAuthenticated } = useAuth();
  const [user, setUser] = useState<UserProfile | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const isMountedRef = useRef(false);

  useEffect(() => {
    isMountedRef.current = true;
    return () => {
      isMountedRef.current = false;
    };
  }, []);

  const fetchUser = useCallback(async () => {
    if (!isAuthenticated) {
      if (isMountedRef.current) {
        setUser(null);
        setLoading(false);
        setError(null);
      }
      return;
    }
    if (isMountedRef.current) {
      setLoading(true);
      setError(null);
    }
    try {
      const res = await apiFetch(userEndpoint);
      if (!res.ok) throw await readApiError(res);
      const body = (await res.json()) as { data?: { user_id?: string; name?: string } };
      if (!isMountedRef.current) return;
      // Avatars are not served: the menu shows initials.
      setUser({ id: body.data?.user_id ?? "", name: body.data?.name ?? "", picture: "" });
    } catch (fetchError) {
      console.error("Failed to load current user", fetchError);
      if (!isMountedRef.current) return;
      setUser(null);
      setError(fetchError instanceof Error ? fetchError.message : "Failed to load current user");
    } finally {
      if (isMountedRef.current) setLoading(false);
    }
  }, [isAuthenticated]);

  useEffect(() => {
    void fetchUser();
  }, [fetchUser]);

  const value = useMemo(
    () => ({ user, loading, error, refresh: fetchUser }),
    [user, loading, error, fetchUser],
  );

  return <UserContext.Provider value={value}>{children}</UserContext.Provider>;
}
