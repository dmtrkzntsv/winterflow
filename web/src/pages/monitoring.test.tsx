import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Outlet, Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import MonitoringPage from "@/pages/monitoring";
import type { ServerSeries } from "@/lib/metrics";

const useServerMetrics = vi.fn();
let data: ServerSeries | null = null;

vi.mock("@/context/use-servers", () => ({
  useServers: () => ({ activeServer: { id: "s1", name: "prod-1" }, activeServerId: "s1" }),
}));
vi.mock("@/context/use-apps", () => ({
  useApps: () => ({ apps: [{ id: "app-1", name: "Nextcloud" }] }),
}));
vi.mock("@/hooks/use-metrics", () => ({
  useServerMetrics: (...args: unknown[]) => {
    useServerMetrics(...args);
    return { data, loading: false, error: null, refresh: vi.fn() };
  },
}));

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

describe("MonitoringPage", () => {
  beforeEach(() => {
    useServerMetrics.mockClear();
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
    expect(useServerMetrics).toHaveBeenLastCalledWith("s1", "24h");
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

  it("refetches for a new range", async () => {
    renderPage();
    await userEvent.click(screen.getByRole("radio", { name: "7 days" }));
    expect(useServerMetrics).toHaveBeenLastCalledWith("s1", "7d");
  });
});
