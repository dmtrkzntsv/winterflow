import { cloneElement, type ReactElement } from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { MetricChartCard, ModeToggle, RangeSelect } from "@/components/metric-chart";
import { Sparkline } from "@/components/sparkline";
import { chartColor } from "@/lib/metrics";

// jsdom has no layout: give recharts' ResponsiveContainer a fixed size so
// the chart (areas, legend) actually renders.
vi.mock("recharts", async (importOriginal) => {
  const mod = await importOriginal<typeof import("recharts")>();
  return {
    ...mod,
    ResponsiveContainer: ({ children }: { children: ReactElement<{ width?: number; height?: number }> }) => (
      <div style={{ width: 600, height: 180 }}>{cloneElement(children, { width: 600, height: 180 })}</div>
    ),
  };
});

const series = [{ key: "cpu", label: "CPU", color: chartColor(0) }];
const rows2 = [
  { t: 0, cpu: 1, mem: 2 },
  { t: 60_000, cpu: 2, mem: 3 },
];

function areaPaths(container: HTMLElement) {
  return [...container.querySelectorAll<SVGPathElement>("path.recharts-area-area")];
}

describe("MetricChartCard", () => {
  it("says No data yet without rows or with only nulls", () => {
    const { rerender } = render(<MetricChartCard title="CPU" rows={[]} series={series} range="1h" format={String} />);
    expect(screen.getByTestId("chart-CPU")).toHaveTextContent("No data yet");
    rerender(<MetricChartCard title="CPU" rows={[{ t: 0, cpu: null }]} series={series} range="1h" format={String} />);
    expect(screen.getByTestId("chart-CPU")).toHaveTextContent("No data yet");
  });

  it("shows an explicit empty message over the chart", () => {
    render(
      <MetricChartCard
        title="CPU"
        rows={[{ t: 0, cpu: 1 }]}
        series={series}
        range="1h"
        format={String}
        empty="Container metrics are unavailable on this host"
      />,
    );
    expect(screen.getByText("Container metrics are unavailable on this host")).toBeInTheDocument();
  });

  it("renders a chart container when there are rows", () => {
    const { container } = render(
      <MetricChartCard title="CPU" rows={[{ t: 0, cpu: 1 }, { t: 60_000, cpu: 2 }]} series={series} range="1h" format={String} />,
    );
    expect(screen.queryByText("No data yet")).not.toBeInTheDocument();
    expect(container.querySelector("[data-chart]")).not.toBeNull();
  });

  it("labels a stacked chart even with a single series", () => {
    const { container } = render(
      <MetricChartCard title="CPU by app" rows={rows2} series={[{ key: "cpu", label: "Nextcloud", color: chartColor(0) }]} range="1h" format={String} stacked />,
    );
    const legend = container.querySelector(".recharts-legend-wrapper");
    expect(legend).not.toBeNull();
    expect(legend).toHaveTextContent("Nextcloud");
  });

  it("hides the legend of a single unstacked series", () => {
    const { container } = render(<MetricChartCard title="CPU" rows={rows2} series={series} range="1h" format={String} />);
    expect(container.querySelector(".recharts-legend-wrapper")).toBeNull();
  });

  it("wraps a long legend inside an overflow-safe card", () => {
    const many = Array.from({ length: 9 }, (_, i) => ({ key: `temp${i}`, label: `Package id ${i}`, color: chartColor(i) }));
    const { container } = render(
      <MetricChartCard title="Temperature" rows={[{ t: 0, temp0: 1 }, { t: 60_000, temp0: 2 }]} series={many} range="1h" format={String} lines />,
    );
    expect(screen.getByTestId("chart-Temperature")).toHaveClass("min-w-0");
    const legend = container.querySelector(".recharts-legend-wrapper > div");
    expect(legend).toHaveClass("flex-wrap");
    expect(legend?.className).toContain("whitespace-nowrap");
  });

  it("draws lines without fills when asked", () => {
    const { container, rerender } = render(
      <MetricChartCard title="Load" rows={rows2} series={[series[0], { key: "mem", label: "Mem", color: chartColor(1) }]} range="1h" format={String} lines />,
    );
    const paths = areaPaths(container);
    expect(paths).toHaveLength(2);
    for (const p of paths) expect(p.getAttribute("fill")).toBe("none");
    rerender(
      <MetricChartCard title="Load" rows={rows2} series={[series[0], { key: "mem", label: "Mem", color: chartColor(1) }]} range="1h" format={String} />,
    );
    for (const p of areaPaths(container)) expect(p.getAttribute("fill")).not.toBe("none");
  });
});

describe("Sparkline", () => {
  it("renders a chart with two or more numeric points", () => {
    render(<Sparkline rows={[{ t: 0, cpu: 1 }, { t: 60_000, cpu: 2 }]} field="cpu" color={chartColor(0)} />);
    expect(screen.getByTestId("sparkline-cpu")).toBeInTheDocument();
  });

  it("renders nothing without enough numeric points", () => {
    const { container, rerender } = render(<Sparkline rows={[]} field="cpu" color={chartColor(0)} />);
    expect(container).toBeEmptyDOMElement();
    rerender(<Sparkline rows={[{ t: 0, cpu: 1 }, { t: 60_000, cpu: null }]} field="cpu" color={chartColor(0)} />);
    expect(container).toBeEmptyDOMElement();
  });
});

describe("RangeSelect and ModeToggle", () => {
  it("report the picked value", async () => {
    const onRange = vi.fn();
    const onMode = vi.fn();
    render(
      <>
        <RangeSelect value="1h" onChange={onRange} />
        <ModeToggle value="avg" onChange={onMode} />
      </>,
    );
    await userEvent.click(screen.getByRole("radio", { name: "7 days" }));
    expect(onRange).toHaveBeenCalledWith("7d");
    await userEvent.click(screen.getByRole("radio", { name: "Peak" }));
    expect(onMode).toHaveBeenCalledWith("peak");
    // Clicking the selected item would unset it; the value stays.
    await userEvent.click(screen.getByRole("radio", { name: "1 hour" }));
    expect(onRange).toHaveBeenCalledTimes(1);
  });
});
