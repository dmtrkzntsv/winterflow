import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { ServerCards } from "@/components/server-cards";
import type { Server } from "@/context/servers-context-base";
import type { ServerOverview } from "@/lib/metrics";

const setActiveServerId = vi.fn();
let servers: Server[] = [];
let overview: ServerOverview[] = [];
let statusByServer: Record<string, "online" | "unknown"> = {};
let loading = false;

vi.mock("@/context/use-servers", () => ({
  useServers: () => ({ servers, activeServerId: "s1", setActiveServerId, statusByServer, loading }),
}));
vi.mock("@/hooks/use-metrics", () => ({
  useMetricsOverview: () => ({ data: { servers: overview }, loading: false, error: null, refresh: vi.fn() }),
}));

const server = (id: string, name: string, lastSeenAt: string | null = null): Server => ({
  id,
  organizationId: "o",
  name,
  createdAt: "",
  lastSeenAt,
  capabilities: {},
});

function renderCards() {
  return render(
    <MemoryRouter>
      <Routes>
        <Route path="/" element={<ServerCards />} />
        <Route path="/monitoring" element={<div>monitoring page</div>} />
      </Routes>
    </MemoryRouter>,
  );
}

describe("ServerCards", () => {
  beforeEach(() => {
    setActiveServerId.mockClear();
    loading = false;
    servers = [server("s1", "prod-1"), server("s2", "backup", "2026-10-11T10:00:00Z")];
    statusByServer = { s1: "online" };
    overview = [
      {
        server_id: "s1",
        liveness: "online",
        latest: {
          at: "2026-10-11T12:00:00Z",
          server: {
            cpu: 42,
            mem_used: 3,
            mem_total: 4,
            swap_used: 0,
            swap_total: 0,
            load1: 0.5,
            load5: 0.25,
            load15: 0.125,
            disks: [
              { mount: "/", used: 10, total: 100 },
              { mount: "/srv", used: 90, total: 100 },
            ],
            temp: 55.4,
          },
        },
        sparkline: [{ t: 0, cpu: 10, mem_used: 1, mem_total: 4, disk_pct: 90 }],
        apps: { running: 3, stopped: 1, problematic: 1 },
      },
    ];
  });

  it("shows meters, load, temperature and app counts", () => {
    renderCards();
    const card = screen.getByTitle("prod-1: online");
    expect(within(card).getByTestId("meter-CPU")).toHaveTextContent("42%");
    expect(within(card).getByTestId("meter-Memory")).toHaveTextContent("75%");
    // The fullest filesystem, its mount in the row's title.
    expect(within(card).getByTestId("meter-Disk")).toHaveTextContent("90%");
    expect(within(card).getByTestId("meter-Disk")).toHaveAttribute("title", "Fullest filesystem: /srv");
    expect(card).toHaveTextContent("Load 0.50 / 0.25 / 0.13");
    expect(card).toHaveTextContent("55°C");
    expect(within(card).getByTestId("app-counts")).toHaveTextContent("3 running · 1 stopped · 1 problematic");
  });

  it("says when a server is offline and has no data", () => {
    renderCards();
    const card = screen.getByTitle("backup: offline");
    // A relative age beside a muted dot; the full date on hover.
    expect(within(card).getByTestId("presence")).toHaveTextContent(/^offline \d+[mhd]$/);
    expect(within(card).getByTestId("presence").getAttribute("title")).toMatch(/^Offline since /);
    expect(card).toHaveTextContent("No data yet");
  });

  it("says just now while the agent is connected, waiting before it ever was", () => {
    servers.push(server("s3", "fresh"));
    renderCards();
    expect(within(screen.getByTitle("prod-1: online")).getByTestId("presence")).toHaveTextContent("just now");
    expect(within(screen.getByTitle("fresh: offline")).getByTestId("presence")).toHaveTextContent("waiting");
  });

  it("falls back to the sparkline's newest CPU minute", () => {
    overview[0].latest!.server.cpu = undefined;
    overview[0].sparkline = [{ t: 0, cpu: 10 }, { t: 60, cpu: 33 }];
    renderCards();
    expect(within(screen.getByTitle("prod-1: online")).getByTestId("meter-CPU")).toHaveTextContent("33%");
  });

  it("shows a card-shaped skeleton while the servers load", () => {
    servers = [];
    loading = true;
    renderCards();
    expect(screen.getByRole("status", { name: "Loading servers" })).toBeInTheDocument();
  });

  it("hides temperature without sensors and problematic when zero", () => {
    overview[0].latest!.server.temp = undefined;
    overview[0].apps.problematic = 0;
    renderCards();
    const card = screen.getByTitle("prod-1: online");
    expect(card).not.toHaveTextContent("°C");
    expect(card).not.toHaveTextContent("problematic");
  });

  it("opens the server's Monitoring page on click", async () => {
    renderCards();
    await userEvent.click(screen.getByTitle("backup: offline"));
    expect(setActiveServerId).toHaveBeenCalledWith("s2");
    expect(await screen.findByText("monitoring page")).toBeInTheDocument();
  });

  it("opens the Monitoring page from the keyboard", async () => {
    renderCards();
    const card = screen.getByRole("link", { name: "backup: offline, open monitoring" });
    card.focus();
    expect(card).toHaveFocus();
    await userEvent.keyboard("{Enter}");
    expect(setActiveServerId).toHaveBeenCalledWith("s2");
    expect(await screen.findByText("monitoring page")).toBeInTheDocument();
  });

  it("keeps the specs line and agent version", () => {
    servers[0].capabilities = { system_cpu_cores: "4", version: "1.2.3", server_ip: "10.0.0.5" };
    renderCards();
    const card = screen.getByTitle("prod-1: online");
    expect(card).toHaveTextContent("4 cores");
    expect(card).toHaveTextContent("Agent v1.2.3");
    expect(card).toHaveTextContent("10.0.0.5");
  });

  it("shows neutral meters and app counts when metrics are off", () => {
    overview[0].latest = null;
    overview[0].sparkline = [];
    renderCards();
    const card = screen.getByTitle("prod-1: online");
    for (const m of ["CPU", "Memory", "Disk"]) {
      expect(within(card).getByTestId(`meter-${m}`)).toHaveTextContent("–");
      expect(within(card).getByTestId(`meter-${m}`)).not.toHaveTextContent("0%");
    }
    expect(within(card).getByTestId("app-counts")).toHaveTextContent("3 running");
  });

  it("renders nothing without servers", () => {
    servers = [];
    const { container } = renderCards();
    expect(container).toBeEmptyDOMElement();
  });
});
