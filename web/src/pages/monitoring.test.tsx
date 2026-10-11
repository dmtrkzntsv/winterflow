import { cloneElement, type ReactElement } from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Outlet, Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import MonitoringPage from "@/pages/monitoring";
import type { DockerDF, ServerSeries } from "@/lib/metrics";

const useServerMetrics = vi.fn();
let data: ServerSeries | null = null;
let latestDF: DockerDF | undefined;

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
vi.mock("@/hooks/use-metrics", () => ({
  useServerMetrics: (...args: unknown[]) => {
    useServerMetrics(...args);
    return { data, loading: false, error: null, refresh: vi.fn() };
  },
  useMetricsOverview: () => ({
    data: {
      servers: [
        { server_id: "other", latest: { at: "", server: { docker_df: { images: 1, volumes: 1, build_cache: 1, reclaimable: 1 } } } },
        { server_id: "s1", latest: latestDF ? { at: "", server: { docker_df: latestDF } } : null },
      ],
    },
    loading: false,
    error: null,
    refresh: vi.fn(),
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
      <Routes>
        <Route element={<Outlet context={{ setBreadcrumbs: vi.fn() }} />}>
          <Route path="/" element={<MonitoringPage />} />
        </Route>
      </Routes>
    </MemoryRouter>,
  );
}

// Rendering every chart through recharts is slow on a busy CI box.
describe("MonitoringPage", { timeout: 20_000 }, () => {
  beforeEach(() => {
    useServerMetrics.mockClear();
    latestDF = undefined;
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

  it("charts the active server and lists Docker disk usage", () => {
    renderPage();
    expect(useServerMetrics).toHaveBeenLastCalledWith("s1", "1h");
    for (const title of ["CPU", "Memory", "Load", "Disk usage", "Disk I/O", "Network", "Temperature", "CPU by app", "Memory by app"]) {
      expect(screen.getByTestId(`chart-${title}`)).toBeInTheDocument();
    }
    expect(screen.getByTestId("docker-df")).toHaveTextContent("Images1.0 GB");
    expect(screen.getByTestId("docker-df")).toHaveTextContent("Reclaimable512 B");
    // Rates were never reported: that chart says so instead of drawing zeros.
    expect(screen.getByTestId("chart-Network")).toHaveTextContent("No data yet");
  });

  it("colors temperature series through CSS-safe keys", () => {
    data!.points = [
      { t: 0, "temp:Core 0": 40, "temp:nvme Composite": 30 },
      { t: 60, "temp:Core 0": 41, "temp:nvme Composite": 31 },
    ];
    renderPage();
    const css = screen.getByTestId("chart-Temperature").querySelector("style")?.textContent ?? "";
    expect(css).toContain("--color-temp0:");
    expect(css).toContain("--color-temp1:");
    for (const m of css.matchAll(/(--color-[^;]*?):\s*var\(/g)) expect(m[1]).toMatch(/^--color-[A-Za-z0-9_-]+$/);
    expect(css).not.toContain("temp:");
  });

  it("hides the temperature chart without sensors and waits for the disk scan", () => {
    data!.points = [{ t: 0, cpu: 1 }];
    renderPage();
    expect(screen.queryByTestId("chart-Temperature")).not.toBeInTheDocument();
    expect(screen.getByText(/No disk scan yet/)).toBeInTheDocument();
  });

  it("shows the latest Docker disk scan whatever the range", () => {
    data!.points = [{ t: 0, cpu: 1 }]; // a coarse range without the scan
    latestDF = { images: 2 * 1024 ** 3, volumes: 0, build_cache: 0, reclaimable: 0 };
    renderPage();
    expect(screen.getByTestId("docker-df")).toHaveTextContent("Images2.0 GB");
  });

  it("draws temperatures and load as lines", () => {
    data!.points = [
      { t: 0, load1: 1, load5: 1, load15: 1, "temp:a": 40, "temp:b": 30 },
      { t: 60, load1: 2, load5: 1, load15: 1, "temp:a": 41, "temp:b": 31 },
    ];
    renderPage();
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
    expect(screen.getByTestId("chart-CPU by app")).toHaveTextContent("app-2-un"); // nameless: id prefix
    await userEvent.click(screen.getByRole("radio", { name: "Peak" }));
    expect(fills("CPU by app").length).toBe(2);
    for (const f of fills("CPU by app")) expect(f).toBe("none");
  });

  it("refetches for a new range", async () => {
    renderPage();
    await userEvent.click(screen.getByRole("radio", { name: "7 days" }));
    expect(useServerMetrics).toHaveBeenLastCalledWith("s1", "7d");
  });
});
