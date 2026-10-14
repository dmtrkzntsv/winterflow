import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { useLocation, useMatch, useNavigate } from "react-router-dom";

import { useAuth } from "@/context/use-auth";
import { useNotifications } from "@/context/use-notifications";
import { apiBaseUrl } from "@/config";
import {
  INBOX_PATH,
  SETTINGS_PATH,
  TEMPLATES_PATH,
  appsPath,
  monitoringPath,
  serverSettingsPath,
} from "@/lib/routes";
import { serverPath, serverRoutePattern } from "@/lib/server-paths";

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
  features?: Record<string, boolean> | null;
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
  features: s.features ?? undefined,
});

export function ServersProvider({ children }: { children: ReactNode }) {
  const { isAuthenticated } = useAuth();
  const { subscribe, connected } = useNotifications();
  const [servers, setServers] = useState<Server[]>([]);
  // The active server comes from the URL (/server/:serverId/...). Pages
  // outside a server's scope (account, members) keep showing the last server
  // the tab looked at, falling back to the first one.
  const urlServerId = useMatch(serverRoutePattern)?.params.serverId ?? null;
  const [lastServerId, setLastServerId] = useState<string | null>(null);
  const navigate = useNavigate();
  const { pathname } = useLocation();
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
    } catch (fetchError) {
      console.error("Failed to load servers", fetchError);
      if (!isMountedRef.current) {
        return;
      }
      setServers([]);
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

  useEffect(() => {
    if (urlServerId) setLastServerId(urlServerId);
  }, [urlServerId]);

  const activeServer = useMemo(() => {
    const byId = (id: string | null) =>
      id ? servers.find((s) => s.id === id) : undefined;
    if (urlServerId) return byId(urlServerId) ?? null;
    return byId(lastServerId) ?? servers[0] ?? null;
  }, [servers, urlServerId, lastServerId]);
  const activeServerId = activeServer?.id ?? null;

  // selectServer moves the tab to another server. Server settings open the
  // new server's General page (a section may not exist there), Monitoring
  // stays Monitoring, the organization's Settings go to the server's
  // settings, and Inbox and Templates (organization-wide) stay where they
  // are with only the active server changing. Every other page (Apps, an
  // app, New app) belongs to one server: the new server's Apps.
  const selectServer = useCallback(
    (id: string) => {
      if (!urlServerId && (pathname.startsWith(INBOX_PATH) || pathname.startsWith(TEMPLATES_PATH))) {
        setLastServerId(id);
        return;
      }
      const sub = urlServerId ? pathname.slice(serverPath(urlServerId).length) : "";
      if (sub.startsWith("/settings") || (!urlServerId && pathname.startsWith(SETTINGS_PATH))) {
        navigate(serverSettingsPath(id));
      } else if (sub.startsWith("/monitoring")) {
        navigate(monitoringPath(id));
      } else {
        navigate(appsPath(id));
      }
    },
    [navigate, pathname, urlServerId],
  );

  const value = useMemo(() => {
    return {
      servers,
      activeServer,
      activeServerId,
      selectServer,
      statusByServer,
      loading,
      error,
      refresh: fetchServers,
    };
  }, [
    servers,
    activeServer,
    activeServerId,
    selectServer,
    statusByServer,
    loading,
    error,
    fetchServers,
  ]);

  return (
    <ServersContext.Provider value={value}>
      {children}
    </ServersContext.Provider>
  );
}
