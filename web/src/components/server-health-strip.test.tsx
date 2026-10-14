import { render, screen, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { ServerHealthStrip } from "@/components/server-health-strip";
import type { Server } from "@/context/servers-context-base";
import type { ServerOverview } from "@/lib/metrics";

let standalone = false;
vi.mock("@/config", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/config")>()),
  get isStandalone() {
    return standalone;
  },
}));

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
    standalone = false;
    statusByServer = { s1: "online" };
    overview = [sample];
  });

  it("shows CPU, memory and the fullest disk now, and leads to Monitoring", () => {
    renderStrip(server("2026-10-13T10:00:00Z"));
    const strip = screen.getByRole("region", { name: "Server health" });
    expect(strip).toHaveTextContent("CPU 42%");
    expect(strip).toHaveTextContent("Memory 38%");
    expect(strip).toHaveTextContent("Disk 55%");
    const link = screen.getByRole("link", { name: "Monitoring" });
    expect(link).toHaveAttribute("href", "/server/s1/monitoring");
    // The whole strip is the link; a phone shows only its "›".
    expect(link).toHaveClass("after:absolute", "after:inset-0");
    expect(within(link).getByText("Monitoring")).toHaveClass("max-sm:hidden");
  });

  it("says when there is no data yet instead of dashes", () => {
    overview = [];
    renderStrip(server("2026-10-13T10:00:00Z"));
    expect(screen.getByRole("region", { name: "Server health" })).toHaveTextContent("No data yet");
    expect(screen.queryByText(/CPU/)).not.toBeInTheDocument();
  });

  it("claims nothing while this server's liveness is not known yet", () => {
    statusByServer = {};
    overview = [];
    renderStrip(server("2026-10-13T10:00:00Z"));
    const strip = screen.getByRole("region", { name: "Server health" });
    expect(strip).toHaveTextContent("Checking this server");
    expect(strip).not.toHaveTextContent("Offline");
    expect(screen.getByRole("link", { name: "Monitoring" })).toBeInTheDocument();
  });

  it("takes liveness from the overview when the servers context has none", () => {
    statusByServer = {};
    renderStrip(server("2026-10-13T10:00:00Z"));
    expect(screen.getByRole("region", { name: "Server health" })).toHaveTextContent("CPU 42%");
  });

  it("leaves out the Disk figure when the sample has no disk", () => {
    overview = [{ ...sample, latest: { ...sample.latest, server: { ...sample.latest.server, disks: [] } } }];
    renderStrip(server("2026-10-13T10:00:00Z"));
    const strip = screen.getByRole("region", { name: "Server health" });
    expect(strip).toHaveTextContent("CPU 42%");
    expect(strip).not.toHaveTextContent("Disk");
  });

  it("treats a standalone server as online without any liveness entry", () => {
    standalone = true;
    statusByServer = {};
    overview = [];
    renderStrip(server(null));
    expect(screen.getByRole("region", { name: "Server health" })).toHaveTextContent("No data yet");
  });

  it("keeps no clock running while the server is online (nothing on screen ages)", () => {
    const every = vi.spyOn(window, "setInterval");
    renderStrip(server("2026-10-13T10:00:00Z"));
    expect(every).not.toHaveBeenCalledWith(expect.any(Function), 60_000);
    every.mockRestore();
  });

  it("says the server is offline and since when", () => {
    statusByServer = { s1: "unknown" };
    const every = vi.spyOn(window, "setInterval");
    renderStrip(server(new Date(Date.now() - 5 * 60_000).toISOString()));
    // Its age keeps counting.
    expect(every).toHaveBeenCalledWith(expect.any(Function), 60_000);
    every.mockRestore();
    const strip = screen.getByRole("region", { name: "Server health" });
    expect(strip).toHaveTextContent("Offline");
    expect(strip).toHaveTextContent("last seen 5m ago");
    expect(strip).not.toHaveTextContent("CPU");
  });

  it("says just now for a server that dropped seconds ago", () => {
    statusByServer = { s1: "unknown" };
    renderStrip(server(new Date(Date.now() - 5_000).toISOString()));
    expect(screen.getByRole("region", { name: "Server health" })).toHaveTextContent("last seen just now");
  });

  it("says a server that never connected is still awaited", () => {
    statusByServer = { s1: "unknown" };
    renderStrip(server(null));
    expect(screen.getByRole("region", { name: "Server health" })).toHaveTextContent(
      "Waiting for this server to connect for the first time",
    );
  });
});
