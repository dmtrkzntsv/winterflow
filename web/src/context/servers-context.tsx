import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";

import { useAuth } from "@/context/use-auth";
import { apiBaseUrl } from "@/config";

import { ServersContext, type Server } from "./servers-context-base";

type ServerResponse = {
  id: string;
  organization_id: string;
  name: string;
  created_at: string;
  last_seen_at: string | null;
};

type GetServersResponse = {
  success: boolean;
  message?: string;
  data?: { servers?: ServerResponse[] | null };
};

const serversEndpoint = (() => {
  const baseUrl = apiBaseUrl.endsWith("/")
    ? apiBaseUrl.slice(0, -1)
    : apiBaseUrl;
  return `${baseUrl}/api/v1/server/get-servers`;
})();

const toServer = (s: ServerResponse): Server => ({
  id: s.id,
  organizationId: s.organization_id,
  name: s.name,
  createdAt: s.created_at,
  lastSeenAt: s.last_seen_at,
});

export function ServersProvider({ children }: { children: ReactNode }) {
  const { isAuthenticated } = useAuth();
  const [servers, setServers] = useState<Server[]>([]);
  const [activeServerId, setActiveServerId] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const isMountedRef = useRef(false);

  useEffect(() => {
    isMountedRef.current = true;
    return () => {
      isMountedRef.current = false;
    };
  }, []);

  const fetchServers = useCallback(async () => {
    if (!isAuthenticated) {
      if (isMountedRef.current) {
        setServers([]);
        setActiveServerId(null);
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
      const response = await fetch(serversEndpoint, {
        credentials: "include",
      });
      if (!response.ok) {
        throw new Error(`Failed to load servers: ${response.status}`);
      }

      const result = (await response.json()) as GetServersResponse;
      if (!isMountedRef.current) {
        return;
      }
      const list = (result.data?.servers ?? []).map(toServer);
      setServers(list);
      // Keep the current selection if it still exists; otherwise default to
      // the first server.
      setActiveServerId((prev) => {
        if (prev && list.some((s) => s.id === prev)) {
          return prev;
        }
        return list[0]?.id ?? null;
      });
    } catch (fetchError) {
      console.error("Failed to load servers", fetchError);
      if (!isMountedRef.current) {
        return;
      }
      setServers([]);
      setActiveServerId(null);
      setError(
        fetchError instanceof Error
          ? fetchError.message
          : "Failed to load servers",
      );
    } finally {
      if (isMountedRef.current) {
        setLoading(false);
      }
    }
  }, [isAuthenticated]);

  useEffect(() => {
    void fetchServers();
  }, [fetchServers]);

  const value = useMemo(() => {
    const activeServer =
      servers.find((s) => s.id === activeServerId) ?? null;
    return {
      servers,
      activeServer,
      activeServerId,
      setActiveServerId,
      loading,
      error,
      refresh: fetchServers,
    };
  }, [servers, activeServerId, loading, error, fetchServers]);

  return (
    <ServersContext.Provider value={value}>
      {children}
    </ServersContext.Provider>
  );
}
