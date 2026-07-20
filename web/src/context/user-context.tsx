import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";

import { useAuth } from "@/context/use-auth";

import { UserContext, type UserProfile } from "./user-context-base";
import { apiBaseUrl } from "@/config";

type UserResponse = {
  id: string;
  name: string;
  picture: string;
  aud?: string;
  attrs?: {
    provider?: string;
    [key: string]: unknown;
  };
};

const resolveUserEndpoint = () => {
  const baseUrl = apiBaseUrl.endsWith("/")
    ? apiBaseUrl.slice(0, -1)
    : apiBaseUrl;
  return `${baseUrl}/auth/user`;
};

const userEndpoint = resolveUserEndpoint();

const resolvePictureUrl = (picture?: string) => {
  if (!picture) {
    return "";
  }
  if (picture.startsWith("http://") || picture.startsWith("https://")) {
    return picture;
  }
  const baseUrl = apiBaseUrl.endsWith("/")
    ? apiBaseUrl.slice(0, -1)
    : apiBaseUrl;
  try {
    return new URL(picture, baseUrl).toString();
  } catch {
    return picture;
  }
};

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
      const response = await fetch(userEndpoint, {
        credentials: "include",
      });
      if (!response.ok) {
        throw new Error(`Failed to load user: ${response.status}`);
      }

      const data = (await response.json()) as UserResponse;
      if (!isMountedRef.current) {
        return;
      }
      setUser({
        id: data.id,
        name: data.name,
        picture: resolvePictureUrl(data.picture),
        provider: data.attrs?.provider,
      });
    } catch (fetchError) {
      console.error("Failed to load current user", fetchError);
      if (!isMountedRef.current) {
        return;
      }
      setUser(null);
      setError(
        fetchError instanceof Error
          ? fetchError.message
          : "Failed to load current user",
      );
    } finally {
      if (isMountedRef.current) {
        setLoading(false);
      }
    }
  }, [isAuthenticated]);

  useEffect(() => {
    void fetchUser();
  }, [fetchUser]);

  const value = useMemo(
    () => ({
      user,
      loading,
      error,
      refresh: fetchUser,
    }),
    [user, loading, error, fetchUser],
  );

  return <UserContext.Provider value={value}>{children}</UserContext.Provider>;
}
