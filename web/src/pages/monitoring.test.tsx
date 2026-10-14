import { cloneElement, type ReactElement } from "react";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import MonitoringPage from "@/pages/monitoring";
import { appLabel } from "@/lib/kpi";
import type { DockerDF, ServerSample, ServerSeries } from "@/lib/metrics";

const useServerMetrics = vi.fn();
const refreshServer = vi.fn(async () => {});
const refreshOverview = vi.fn(async () => {});
let data: ServerSeries | null = null;
let loading = false;
let latestDF: DockerDF | undefined;
let latestServer: Partial<ServerSample> | undefined;

// jsdom has no layout: a fixed-size ResponsiveContainer makes recharts draw.
vi.mock("recharts", async (importOriginal) => {
  const mod = await importOriginal<typeof import("recharts")>();
  return {
    ...mod,
    ResponsiveContainer: ({ children }: { children: ReactElement<{ width?: number; height?: number }> }) => (
      <div style={{ width: 600, height: 180 }}>{cloneElement(children, { width: 600, height: 180 })}</div>
    ),
  };
});

vi.mock("@/context/use-servers", () => ({
  useServers: () => ({ activeServer: { id: "s1", name: "prod-1" }, activeServerId: "s1" }),
}));
vi.mock("@/context/use-apps", () => ({
  useApps: () => ({ apps: [{ id: "app-1", name: "Nextcloud" }, { id: "app-2-unnamed", name: "" }] }),
}));
// The alert components have their own tests; here they only have to be placed.
vi.mock("@/components/alerts/target-alerts-card", () => ({
  TargetAlertsCard: (p: { serverId: string; appId?: string }) => (
    <div data-testid="alerts-card">{`${p.serverId}/${p.appId ?? ""}`}</div>
  ),
}));
vi.mock("@/components/alerts/firing-banner", () => ({
  FiringBanner: (p: { serverId: string; appId?: string }) => <div data-testid="firing-banner">{p.serverId}</div>,
}));
vi.mock("@/hooks/use-metrics", () => ({
  useServerMetrics: (...args: unknown[]) => {
    useServerMetrics(...args);
    return { data, loading, error: null, updatedAt: data ? Date.parse("2026-10-11T17:06:00") : null, refresh: refreshServer };
  },
  useMetricsOverview: () => ({
    data: {
      servers: [
        { server_id: "other", latest: { at: "", server: { docker_df: { images: 1, volumes: 1, build_cache: 1, reclaimable: 1 } } } },
        {
          server_id: "s1",
          latest: latestDF || latestServer ? { at: "", server: { ...latestServer, docker_df: latestDF } } : null,
        },
      ],
    },
    loading: false,
    error: null,
    updatedAt: null,
    refresh: refreshOverview,
  }),
}));

function fills(title: string): (string | null)[] {
  return [...screen.getByTestId(`chart-${title}`).querySelectorAll("path.recharts-area-area")].map((p) =>
    p.getAttribute("fill"),
  );
}

function renderPage() {
  return render(
    <MemoryRouter>
      <MonitoringPage />
    </MemoryRouter>,
  );
}

const openDetails = () => userEvent.click(screen.getByRole("button", { name: "More details" }));

// Rendering every chart through recharts is slow on a busy CI box.
describe("MonitoringPage", { timeout: 20_000 }, () => {
  beforeEach(() => {
    useServerMetrics.mockClear();
    refreshServer.mockClear();
    refreshOverview.mockClear();
    loading = false;
    latestDF = undefined;
    latestServer = undefined;
    data = {
      interval_seconds: 60,
      points: [
        { t: 0, cpu: 5, mem_used: 1, mem_total: 4, load1: 1, "temp:Composite": 40 },
        {
          t: 60,
          cpu: 6,
          mem_used: 2,
          mem_total: 4,
          load1: 1,
          "temp:Composite": 41,
          docker_images: 1024 ** 3,
          docker_volumes: 0,
          docker_build_cache: 0,
          docker_reclaimable: 512,
        },
      ],
      apps: [{ app_id: "app-1", points: [{ t: 0, cpu: 1, mem_used: 5 }] }],
    };
  });

  it("heads the page with the server, freshness and the range, then the firing banner", () => {
    renderPage();
    const header = screen.getByRole("banner");
    expect(within(header).getByRole("heading", { level: 1, name: "Monitoring" })).toBeInTheDocument();
    expect(header).toHaveTextContent("prod-1");
    // Freshness sits in the subtitle paragraph: phrasing content only.
    expect(screen.getByTestId("data-as-of").closest("p")).not.toBeNull();
    expect(within(header).getByRole("button", { name: "Range: Last hour" })).toBeInTheDocument();
    const banner = screen.getByTestId("firing-banner");
    expect(banner).toHaveTextContent("s1");
    expect(banner.compareDocumentPosition(screen.getByTestId("kpi-CPU")) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it("shows four tiles, the four host charts and the apps, with the rest behind More details", async () => {
    renderPage();
    expect(useServerMetrics).toHaveBeenLastCalledWith("s1", "1h");
    for (const label of ["CPU", "Memory", "Disk", "Network"]) expect(screen.getByTestId(`kpi-${label}`)).toBeInTheDocument();
    const host = screen.getByTestId("host-charts");
    for (const title of ["CPU", "Memory", "Disk usage", "Network"]) {
      expect(within(host).getByTestId(`chart-${title}`)).toBeInTheDocument();
    }
    const apps = screen.getByRole("region", { name: "Apps" });
    for (const title of ["CPU by app", "Memory by app"]) expect(within(apps).getByTestId(`chart-${title}`)).toBeInTheDocument();
    for (const title of ["Load", "Disk I/O", "Temperature"]) expect(screen.queryByTestId(`chart-${title}`)).not.toBeInTheDocument();
    expect(screen.queryByTestId("docker-df")).not.toBeInTheDocument();
    // Rates were never reported: that chart says so instead of drawing zeros.
    expect(screen.getByTestId("chart-Network")).toHaveTextContent("No data yet");
    expect(screen.getByTestId("alerts-card")).toHaveTextContent("s1/");

    await openDetails();
    for (const title of ["Load", "Disk I/O", "Temperature"]) expect(screen.getByTestId(`chart-${title}`)).toBeInTheDocument();
    expect(screen.getByTestId("docker-df")).toHaveTextContent("Images1.0 GB");
    expect(screen.getByTestId("docker-df")).toHaveTextContent("Reclaimable512 B");
  });

  it("stacks the charts in one column on a phone", () => {
    renderPage();
    for (const grid of [screen.getByTestId("host-charts"), within(screen.getByRole("region", { name: "Apps" })).getByTestId("app-charts")]) {
      expect(grid).toHaveClass("grid", "md:grid-cols-2");
      expect(grid).not.toHaveClass("grid-cols-2");
    }
  });

  it("colors temperature series through CSS-safe keys", async () => {
    data!.points = [
      { t: 0, "temp:Core 0": 40, "temp:nvme Composite": 30 },
      { t: 60, "temp:Core 0": 41, "temp:nvme Composite": 31 },
    ];
    renderPage();
    await openDetails();
    const css = screen.getByTestId("chart-Temperature").querySelector("style")?.textContent ?? "";
    expect(css).toContain("--color-temp0:");
    expect(css).toContain("--color-temp1:");
    for (const m of css.matchAll(/(--color-[^;]*?):\s*var\(/g)) expect(m[1]).toMatch(/^--color-[A-Za-z0-9_-]+$/);
    expect(css).not.toContain("temp:");
  });

  it("explains a fresh server in plain words: no sensors, no disk scan yet, no app samples", async () => {
    data = { interval_seconds: 60, points: [{ t: 0, cpu: 1 }], apps: [] };
    renderPage();
    expect(screen.getByTestId("chart-CPU by app")).toHaveTextContent("No data yet");
    await openDetails();
    expect(screen.queryByTestId("chart-Temperature")).not.toBeInTheDocument();
    expect(screen.getByText("No disk scan yet. It runs once an hour.")).toBeInTheDocument();
    expect(document.body).not.toHaveTextContent(/[A-Z]{3,}_[A-Z]{3,}/);
  });

  it("shows the latest Docker disk scan whatever the range", async () => {
    data!.points = [{ t: 0, cpu: 1 }]; // a coarse range without the scan
    latestDF = { images: 2 * 1024 ** 3, volumes: 0, build_cache: 0, reclaimable: 0 };
    renderPage();
    await openDetails();
    expect(screen.getByTestId("docker-df")).toHaveTextContent("Images2.0 GB");
  });

  it("draws temperatures and load as lines", async () => {
    data!.points = [
      { t: 0, load1: 1, load5: 1, load15: 1, "temp:a": 40, "temp:b": 30 },
      { t: 60, load1: 2, load5: 1, load15: 1, "temp:a": 41, "temp:b": 31 },
    ];
    renderPage();
    await openDetails();
    for (const title of ["Temperature", "Load"]) {
      expect(fills(title).length).toBeGreaterThan(0);
      for (const f of fills(title)) expect(f).toBe("none");
    }
  });

  it("stacks per-app averages but draws per-app peaks as lines", async () => {
    data!.apps = [
      { app_id: "app-1", points: [{ t: 0, cpu: 1, cpu_max: 3 }, { t: 60, cpu: 2, cpu_max: 4 }] },
      { app_id: "app-2-unnamed", points: [{ t: 0, cpu: 1, cpu_max: 3 }, { t: 60, cpu: 2, cpu_max: 4 }] },
    ];
    renderPage();
    for (const f of fills("CPU by app")) expect(f).not.toBe("none");
    expect(screen.getByTestId("chart-CPU by app")).toHaveTextContent("Nextcloud");
    // A nameless app reads "Unnamed app", never its raw id.
    expect(screen.getByTestId("chart-CPU by app")).toHaveTextContent(appLabel(""));
    const chart = screen.getByTestId("chart-CPU by app");
    const shown = (chart.textContent ?? "").replace(chart.querySelector("style")?.textContent ?? "", "");
    expect(shown).not.toContain("app-2-un");
    await userEvent.click(screen.getByRole("radio", { name: "Peak" }));
    expect(fills("CPU by app").length).toBe(2);
    for (const f of fills("CPU by app")) expect(f).toBe("none");
  });

  it("refetches for a new range", async () => {
    renderPage();
    await userEvent.click(screen.getByRole("button", { name: "Range: Last hour" }));
    await userEvent.click(await screen.findByRole("menuitemradio", { name: "Last 7 days" }));
    expect(useServerMetrics).toHaveBeenLastCalledWith("s1", "7d");
  });

  it("leads with KPI tiles: the newest value and what it is out of, no sparkline or trend pill", () => {
    data!.points = Array.from({ length: 60 }, (_, i) =>
      i < 30
        ? { t: i * 60, cpu: 10, mem_used: 1, mem_total: 4, net_rx_bps: 100, net_tx_bps: 100 }
        : { t: i * 60, cpu: 20, mem_used: 2, mem_total: 4, net_rx_bps: 1024, net_tx_bps: 1024 },
    );
    renderPage();
    const cpu = screen.getByTestId("kpi-CPU");
    expect(cpu).toHaveTextContent("20%");
    // The charts below carry the trend.
    expect(screen.getByTestId("kpi-grid").querySelector("[data-tone], [data-testid^=sparkline]")).toBeNull();
    expect(screen.getByTestId("kpi-Memory")).toHaveTextContent("50%");
    expect(screen.getByTestId("kpi-Memory")).toHaveTextContent("2 B of 4 B");
    expect(screen.getByTestId("kpi-Network")).toHaveTextContent("2.0 KB/s");
    expect(screen.getByTestId("kpi-Network")).toHaveTextContent("In 1.0 KB/s · Out 1.0 KB/s");
    // No disk series at all: no figure rather than a wild one.
    expect(screen.getByTestId("kpi-Disk")).toHaveTextContent("–");
  });

  it("takes headline figures from the live sample, not the range's last bucket", () => {
    latestServer = {
      cpu: 42,
      mem_used: 3,
      mem_total: 4,
      load1: 0.5,
      disks: [{ mount: "/", used: 1, total: 4 }],
      net_rx_bps: 2048,
      net_tx_bps: 0,
    };
    renderPage();
    expect(screen.getByTestId("kpi-CPU")).toHaveTextContent("42%");
    expect(screen.getByTestId("kpi-CPU")).toHaveTextContent("Load 0.50");
    expect(screen.getByTestId("kpi-Memory")).toHaveTextContent("75%");
    expect(screen.getByTestId("kpi-Memory")).toHaveTextContent("3 B of 4 B");
    expect(screen.getByTestId("kpi-Disk")).toHaveTextContent("25%");
    // Used of total, without the mount point.
    expect(screen.getByTestId("kpi-Disk")).toHaveTextContent(/1 B of 4 B$/);
    expect(screen.getByTestId("kpi-Network")).toHaveTextContent("2.0 KB/s");
  });

  it("states freshness and re-fetches on refresh", async () => {
    renderPage();
    expect(screen.getByTestId("data-as-of")).toHaveTextContent(/^Data as of /);
    await userEvent.click(screen.getByRole("button", { name: "Refresh data" }));
    expect(refreshServer).toHaveBeenCalledTimes(1);
    expect(refreshOverview).toHaveBeenCalledTimes(1);
  });

  it("keeps focus on the refresh button and ignores presses while refreshing", async () => {
    let finish: () => void = () => {};
    refreshServer.mockImplementationOnce(() => new Promise<void>((r) => (finish = r)));
    renderPage();
    const button = screen.getByRole("button", { name: "Refresh data" });
    button.focus();
    await userEvent.keyboard("{Enter}");
    expect(button).toHaveFocus();
    expect(button).toHaveAttribute("aria-busy", "true");
    await userEvent.keyboard("{Enter}");
    expect(refreshServer).toHaveBeenCalledTimes(1);
    finish();
    await waitFor(() => expect(button).toHaveAttribute("aria-busy", "false"));
  });

  it("shows skeletons shaped like the tiles and charts while loading", () => {
    data = null;
    loading = true;
    renderPage();
    // The CPU tile and the CPU chart.
    expect(screen.getAllByRole("status", { name: "Loading CPU" })).toHaveLength(2);
    expect(screen.getAllByRole("status").length).toBeGreaterThan(4);
    expect(screen.queryByTestId("chart-CPU")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "More details" })).not.toBeInTheDocument();
    expect(screen.getByTestId("data-as-of")).toHaveTextContent("Loading data");
  });
});
