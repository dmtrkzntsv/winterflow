import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { AppMetricBlocks, AppMetricsTab } from "@/components/app-metrics";
import type { Series } from "@/lib/metrics";

const calls: unknown[][] = [];
const refresh = vi.fn(async () => {});
let data: Series | null = null;
let loading = false;

// The Alerts card has its own tests; here it only has to be placed.
vi.mock("@/components/alerts/target-alerts-card", () => ({
  TargetAlertsCard: (p: { serverId: string; appId?: string }) => (
    <div data-testid="alerts-card">{`${p.serverId}/${p.appId ?? ""}`}</div>
  ),
}));
vi.mock("@/hooks/use-metrics", () => ({
  useAppMetrics: (...args: unknown[]) => {
    calls.push(args);
    return { data, loading, error: null, updatedAt: data ? 1 : null, refresh };
  },
}));

describe("AppMetricBlocks", () => {
  beforeEach(() => {
    calls.length = 0;
    loading = false;
    data = {
      interval_seconds: 60,
      points: [
        { t: 0, cpu: 1 },
        { t: 60, cpu: 12.5, mem_used: 512 * 1024 ** 2, mem_limit: 1024 ** 3, disk: 2 * 1024 ** 3, image: 1024 ** 2, net_rx_bps: 1024, net_tx_bps: 1024 },
      ],
    };
  });

  it("shows the newest minute", () => {
    render(<AppMetricBlocks serverId="s1" appId="a1" />);
    expect(calls[0]).toEqual(["s1", "a1", "1h"]);
    expect(screen.getByTestId("kpi-CPU")).toHaveTextContent("13%");
    expect(screen.getByTestId("kpi-Memory")).toHaveTextContent("512.0 MBof 1.0 GB limit");
    expect(screen.getByTestId("kpi-Disk")).toHaveTextContent("2.0 GB+ 1.0 MB images");
    expect(screen.getByTestId("kpi-Network")).toHaveTextContent("2.0 KB/s");
    expect(screen.getByTestId("kpi-Network")).toHaveTextContent("In 1.0 KB/s · Out 1.0 KB/s");
  });

  it("reads past a newest point that lacks a field", () => {
    data!.points = [...data!.points, { t: 120, mem_used: 600 * 1024 ** 2 }];
    render(<AppMetricBlocks serverId="s1" appId="a1" />);
    expect(screen.getByTestId("kpi-CPU")).toHaveTextContent("13%");
    expect(screen.getByTestId("kpi-Memory")).toHaveTextContent("600.0 MB");
    expect(screen.getByTestId("kpi-Network")).toHaveTextContent("2.0 KB/s");
  });

  it("lays the four blocks out in pairs", () => {
    render(<AppMetricBlocks serverId="s1" appId="a1" />);
    const grid = screen.getByTestId("kpi-CPU").parentElement!;
    expect(grid).toHaveClass("grid-cols-2");
    expect(within(grid).getAllByTestId(/^kpi-/)).toHaveLength(4);
  });

  it("shows tile skeletons until the first fetch lands", () => {
    data = null;
    loading = true;
    render(<AppMetricBlocks serverId="s1" appId="a1" />);
    expect(screen.getAllByRole("status")).toHaveLength(4);
    expect(screen.getByRole("status", { name: "Loading memory" })).toBeInTheDocument();
  });

  it("says no limit, and dashes for fields not reported yet", () => {
    data!.points = [{ t: 0, mem_used: 1 }];
    render(<AppMetricBlocks serverId="s1" appId="a1" />);
    expect(screen.getByTestId("kpi-Memory")).toHaveTextContent("no limit");
    expect(screen.getByTestId("kpi-CPU")).toHaveTextContent("–");
    expect(screen.getByTestId("kpi-Network")).toHaveTextContent("–");
  });

  it("says there is no data yet, and explains unavailable containers", () => {
    data = { interval_seconds: 60, points: [] };
    const { rerender } = render(<AppMetricBlocks serverId="s1" appId="a1" />);
    expect(screen.getByText("No data yet.")).toBeInTheDocument();
    expect(screen.queryByTestId("kpi-CPU")).not.toBeInTheDocument();
    data = { interval_seconds: 60, points: [{ t: 0, unavailable: 1, unavailable_max: 1 }] };
    rerender(<AppMetricBlocks serverId="s1" appId="a2" />);
    expect(screen.getByText("Container metrics are unavailable on this host")).toBeInTheDocument();
    expect(screen.queryByTestId("kpi-CPU")).not.toBeInTheDocument();
  });

  it("still shows disk when container metrics are unavailable", () => {
    data = { interval_seconds: 60, points: [{ t: 0, unavailable: 1, disk: 2 * 1024 ** 3 }] };
    render(<AppMetricBlocks serverId="s1" appId="a1" />);
    expect(screen.getByText("Container metrics are unavailable on this host")).toBeInTheDocument();
    expect(screen.getByTestId("kpi-Disk")).toHaveTextContent("2.0 GB");
    expect(screen.queryByTestId("kpi-CPU")).not.toBeInTheDocument();
    expect(screen.queryByTestId("kpi-Memory")).not.toBeInTheDocument();
    expect(screen.queryByTestId("kpi-Network")).not.toBeInTheDocument();
  });
});

describe("AppMetricsTab", () => {
  beforeEach(() => {
    calls.length = 0;
    loading = false;
    refresh.mockClear();
    data = { interval_seconds: 1200, points: [{ t: 0, cpu: 1, disk: 5 }] };
  });

  it("charts the app over the chosen range", async () => {
    render(<AppMetricsTab serverId="s1" appId="a1" />);
    expect(calls.at(-1)).toEqual(["s1", "a1", "1h"]);
    // The alert rules card folds itself: it is always placed, never wrapped.
    expect(screen.getByTestId("alerts-card")).toHaveTextContent("s1/a1");
    expect(screen.queryByRole("button", { name: "Alert rules for this app" })).not.toBeInTheDocument();
    for (const title of ["CPU", "Memory", "Network", "Disk"]) {
      expect(screen.getByTestId(`chart-${title}`)).toBeInTheDocument();
    }
    expect(screen.getByTestId("chart-Memory")).toHaveTextContent("No data yet");
    await userEvent.click(screen.getByRole("button", { name: "Range: Last hour" }));
    await userEvent.click(await screen.findByRole("menuitemradio", { name: "Last 30 days" }));
    expect(calls.at(-1)).toEqual(["s1", "a1", "30d"]);
  });

  it("ignores a bucket where containers were unavailable only briefly", () => {
    data = { interval_seconds: 1200, points: [{ t: 0, cpu: 1, mem_used: 5, unavailable: 0.05, unavailable_max: 1 }] };
    render(<AppMetricsTab serverId="s1" appId="a1" />);
    expect(screen.queryByText("Container metrics are unavailable on this host")).not.toBeInTheDocument();
  });

  it("draws a memory limit only when the app has one", () => {
    data = { interval_seconds: 60, points: [{ t: 0, mem_used: 5 }, { t: 60, mem_used: 6 }] };
    const { rerender } = render(<AppMetricsTab serverId="s1" appId="a1" />);
    const css = () => screen.getByTestId("chart-Memory").querySelector("style")?.textContent ?? "";
    expect(css()).toContain("--color-mem_used:");
    expect(css()).not.toContain("--color-mem_limit:");
    data = { interval_seconds: 60, points: [{ t: 0, mem_used: 5, mem_limit: 10 }, { t: 60, mem_used: 6, mem_limit: 10 }] };
    rerender(<AppMetricsTab serverId="s1" appId="a2" />);
    expect(css()).toContain("--color-mem_limit:");
  });

  it("explains unavailable container metrics but still charts disk", () => {
    data = { interval_seconds: 60, points: [{ t: 0, unavailable: 1, disk: 5 }] };
    render(<AppMetricsTab serverId="s1" appId="a1" />);
    expect(screen.getByTestId("chart-CPU")).toHaveTextContent("Container metrics are unavailable on this host");
    expect(screen.getByTestId("chart-Disk")).not.toHaveTextContent("unavailable");
  });

  it("shows chart skeletons while loading and re-fetches on refresh", async () => {
    data = null;
    loading = true;
    const { rerender } = render(<AppMetricsTab serverId="s1" appId="a1" />);
    expect(screen.getAllByRole("status")).toHaveLength(4);
    expect(screen.queryByTestId("chart-CPU")).not.toBeInTheDocument();
    data = { interval_seconds: 60, points: [{ t: 0, cpu: 1 }] };
    loading = false;
    rerender(<AppMetricsTab serverId="s1" appId="a1" />);
    expect(screen.getByTestId("chart-CPU")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Refresh data" }));
    expect(refresh).toHaveBeenCalledTimes(1);
  });
});
