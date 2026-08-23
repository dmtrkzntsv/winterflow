import { createContext } from "react";

export type Server = {
  id: string;
  organizationId: string;
  name: string;
  createdAt: string;
  lastSeenAt: string | null;
  // Agent-reported facts (server_ip, system_cpu_cores, system_memory_total,
  // system_disk_total, version, os, arch, hostname, public_key, ...).
  capabilities: Record<string, string>;
  // Agent-reported feature flags (e.g. ingress). Absent = unknown agent
  // version; treat as unsupported.
  features?: Record<string, boolean>;
};

export type ServerLiveness = "online" | "unknown";

export type ServersContextValue = {
  servers: Server[];
  activeServer: Server | null;
  activeServerId: string | null;
  setActiveServerId: (id: string) => void;
  // Live liveness per server id: seeded from get-servers-status, then updated
  // by server_status SSE notifications. Missing entry = unknown.
  statusByServer: Record<string, ServerLiveness>;
  loading: boolean;
  error: string | null;
  refresh: () => Promise<void>;
};

export const ServersContext = createContext<ServersContextValue | undefined>(
  undefined,
);
