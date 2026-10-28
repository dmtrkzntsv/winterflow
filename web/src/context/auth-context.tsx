import { useCallback, useEffect, useMemo, useState, type ReactNode } from "react";

import { apiBase } from "@/config";
import { apiFetch, jsonPost, readApiError } from "@/lib/api";
import { AuthContext, LoginError, type LoginPayload } from "./auth-context-base";

// AuthProvider knows whether this browser has a session (the wf_session
// cookie, HttpOnly: it is probed, never read) and signs in and out.
export function AuthProvider({ children }: { children: ReactNode }) {
  const [isAuthenticated, setIsAuthenticated] = useState(false);
  const [checked, setChecked] = useState(false);

  const login = useCallback(async ({ email, password }: LoginPayload) => {
    const res = await apiFetch(`${apiBase}/api/v1/auth/login`, jsonPost({ email, password }));
    if (!res.ok) throw new LoginError(res.status);
    setIsAuthenticated(true);
  }, []);

  const signedIn = useCallback(() => setIsAuthenticated(true), []);

  // signOut posts to path; the browser is signed out here whatever the
  // answer (a session the server no longer knows is gone anyway).
  const signOut = useCallback(async (path: string) => {
    try {
      const res = await apiFetch(`${apiBase}${path}`, { method: "POST" });
      if (!res.ok) throw await readApiError(res);
    } catch (error) {
      console.error("Failed to sign out", error);
    } finally {
      setIsAuthenticated(false);
    }
  }, []);
  const logout = useCallback(() => signOut("/api/v1/auth/logout"), [signOut]);
  const logoutAll = useCallback(() => signOut("/api/v1/user/logout-all"), [signOut]);

  useEffect(() => {
    let cancelled = false;
    apiFetch(`${apiBase}/api/v1/user/get-profile`)
      .then(
        (res) => {
          if (!cancelled) setIsAuthenticated(res.ok);
        },
        (error: unknown) => {
          console.error("Failed to check the session", error);
          if (!cancelled) setIsAuthenticated(false);
        },
      )
      .finally(() => {
        if (!cancelled) setChecked(true);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const value = useMemo(
    () => ({ isAuthenticated, checked, login, signedIn, logout, logoutAll }),
    [isAuthenticated, checked, login, signedIn, logout, logoutAll],
  );

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}
