import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";

import { useAuth } from "@/context/use-auth";
import { useNotifications } from "@/context/use-notifications";
import { apiBaseUrl } from "@/config";

import {
  ServersContext,
  type Server,
  type ServerLiveness,
} from "./servers-context-base";

type ServerResponse = {
  id: string;
  organization_id: string;
  name: string;
  created_at: string;
  last_seen_at: string | null;
  capabilities?: { name: string; value: string }[] | null;
};

type GetServersResponse = {
  success: boolean;
  message?: string;
  data?: { servers?: ServerResponse[] | null };
};

type GetServersStatusResponse = {
  data?: { servers?: { server_id: string; liveness: ServerLiveness }[] | null };
};

const baseUrl = apiBaseUrl.endsWith("/") ? apiBaseUrl.slice(0, -1) : apiBaseUrl;
const serversEndpoint = `${baseUrl}/api/v1/server/get-servers`;
const statusEndpoint = `${baseUrl}/api/v1/server/get-servers-status`;

const toServer = (s: ServerResponse): Server => ({
  id: s.id,
  organizationId: s.organization_id,
  name: s.name,
  createdAt: s.created_at,
  lastSeenAt: s.last_seen_at,
  capabilities: Object.fromEntries(
    (s.capabilities ?? []).map((c) => [c.name, c.value]),
  ),
});

export function ServersProvider({ children }: { children: ReactNode }) {
  const { isAuthenticated } = useAuth();
  const { subscribe, connected } = useNotifications();
  const [servers, setServers] = useState<Server[]>([]);
  const [activeServerId, setActiveServerId] = useState<string | null>(null);
  const [statusByServer, setStatusByServer] = useState<
    Record<string, ServerLiveness>
  >({});
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
        setStatusByServer({});
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

  // Seed liveness on login and re-seed when the SSE stream (re)connects — a
  // dropped stream can miss a server_status transition. Steady-state updates
  // are pushed, not polled.
  useEffect(() => {
    if (!isAuthenticated) return;
    let cancelled = false;
    fetch(statusEndpoint, { credentials: "include" })
      .then((r) => (r.ok ? (r.json() as Promise<GetServersStatusResponse>) : null))
      .then((body) => {
        if (cancelled || !body) return;
        const next: Record<string, ServerLiveness> = {};
        for (const s of body.data?.servers ?? []) {
          next[s.server_id] = s.liveness;
        }
        setStatusByServer(next);
      })
      .catch(() => {
        // Seed failure is benign: SSE events will fill the map.
      });
    return () => {
      cancelled = true;
    };
  }, [isAuthenticated, connected]);

  useEffect(() => {
    if (!isAuthenticated) return;
    return subscribe((n) => {
      if (n.type !== "server_status") return;
      const p = n.payload as
        | { server_id?: string; liveness?: ServerLiveness }
        | undefined;
      if (!p?.server_id || !p.liveness) return;
      let cameOnline = false;
      setStatusByServer((prev) => {
        cameOnline =
          p.liveness === "online" && prev[p.server_id!] !== "online";
        return { ...prev, [p.server_id!]: p.liveness! };
      });
      // A server coming online often means fresh last_seen/capabilities
      // (reconnect, agent update) — re-read the durable info.
      if (cameOnline) void fetchServers();
    });
  }, [isAuthenticated, subscribe, fetchServers]);

  const value = useMemo(() => {
    const activeServer =
      servers.find((s) => s.id === activeServerId) ?? null;
    return {
      servers,
      activeServer,
      activeServerId,
      setActiveServerId,
      statusByServer,
      loading,
      error,
      refresh: fetchServers,
    };
  }, [servers, activeServerId, statusByServer, loading, error, fetchServers]);

  return (
    <ServersContext.Provider value={value}>
      {children}
    </ServersContext.Provider>
  );
}
