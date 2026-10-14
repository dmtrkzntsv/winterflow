import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { ServerHealthStrip } from "@/components/server-health-strip";
import type { Server } from "@/context/servers-context-base";
import type { ServerOverview } from "@/lib/metrics";

let statusByServer: Record<string, "online" | "unknown"> = {};
let overview: ServerOverview[] = [];
vi.mock("@/context/use-servers", () => ({ useServers: () => ({ statusByServer }) }));
vi.mock("@/hooks/use-metrics", () => ({
  useMetricsOverview: () => ({ data: { servers: overview }, loading: false, error: null, refresh: vi.fn() }),
}));

const server = (lastSeenAt: string | null): Server => ({
  id: "s1",
  organizationId: "o",
  name: "homelab",
  createdAt: "",
  lastSeenAt,
  capabilities: {},
});

const sample = {
  server_id: "s1",
  liveness: "online" as const,
  latest: {
    at: "2026-10-13T10:00:00Z",
    server: {
      cpu: 42,
      mem_used: 3,
      mem_total: 8,
      swap_used: 0,
      swap_total: 0,
      load1: 0.4,
      load5: 0.3,
      load15: 0.2,
      disks: [
        { mount: "/", used: 55, total: 100 },
        { mount: "/mnt/a", used: 10, total: 100 },
      ],
    },
  },
  sparkline: [],
  apps: { running: 2, stopped: 0, problematic: 0 },
};

function renderStrip(s: Server) {
  return render(
    <MemoryRouter>
      <ServerHealthStrip server={s} />
    </MemoryRouter>,
  );
}

describe("ServerHealthStrip", () => {
  beforeEach(() => {
    statusByServer = { s1: "online" };
    overview = [sample];
  });

  it("shows CPU, memory and the fullest disk now, and leads to Monitoring", () => {
    renderStrip(server("2026-10-13T10:00:00Z"));
    const strip = screen.getByRole("region", { name: "Server health" });
    expect(strip).toHaveTextContent("CPU 42%");
    expect(strip).toHaveTextContent("Memory 38%");
    expect(strip).toHaveTextContent("Disk 55%");
    expect(screen.getByRole("link", { name: "Monitoring" })).toHaveAttribute("href", "/server/s1/monitoring");
  });

  it("says when there is no data yet instead of dashes", () => {
    overview = [];
    renderStrip(server("2026-10-13T10:00:00Z"));
    expect(screen.getByRole("region", { name: "Server health" })).toHaveTextContent("No data yet");
    expect(screen.queryByText(/CPU/)).not.toBeInTheDocument();
  });

  it("says the server is offline and since when", () => {
    statusByServer = {};
    renderStrip(server(new Date(Date.now() - 5 * 60_000).toISOString()));
    const strip = screen.getByRole("region", { name: "Server health" });
    expect(strip).toHaveTextContent("Offline");
    expect(strip).toHaveTextContent("last seen 5m ago");
    expect(strip).not.toHaveTextContent("CPU");
  });

  it("says a server that never connected is still awaited", () => {
    statusByServer = {};
    renderStrip(server(null));
    expect(screen.getByRole("region", { name: "Server health" })).toHaveTextContent(
      "Waiting for this server to connect for the first time",
    );
  });
});
