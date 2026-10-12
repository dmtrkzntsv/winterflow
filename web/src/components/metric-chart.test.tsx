import { cloneElement, type ReactElement } from "react";
import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { ChartCardSkeleton, MetricChartCard } from "@/components/metric-chart";
import { MetricTooltip } from "@/components/chart-parts";
import { Sparkline } from "@/components/sparkline";
import { chartColor, formatPercent, formatSize } from "@/lib/metrics";

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
    const { container, rerender } = render(<MetricChartCard title="CPU" rows={[]} series={series} range="1h" format={String} />);
    expect(screen.getByTestId("chart-CPU")).toHaveTextContent("No data yet");
    // An icon and one line, never an empty axis.
    expect(screen.getByTestId("chart-empty").querySelector("svg")).not.toBeNull();
    expect(container.querySelector("[data-chart]")).toBeNull();
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

  it("is a widget card with a muted title and an info hint", () => {
    render(<MetricChartCard title="CPU" description="Percent of the whole host" rows={rows2} series={series} range="1h" format={String} />);
    const card = screen.getByTestId("chart-CPU");
    expect(card).toHaveAttribute("data-slot", "widget-card");
    expect(screen.getByRole("heading", { name: "CPU" })).toBeInTheDocument();
    // The hint is a focusable button, not a mouse-only title.
    const hint = screen.getByRole("button", { name: "Percent of the whole host" });
    hint.focus();
    expect(hint).toHaveFocus();
  });

  it("sizes the plot by height, or fills the card", () => {
    const { container, rerender } = render(
      <MetricChartCard title="CPU" rows={rows2} series={series} range="1h" format={String} height={320} />,
    );
    expect(container.querySelector("[data-chart]")?.parentElement).toHaveStyle({ height: "320px" });
    rerender(<MetricChartCard title="CPU" rows={rows2} series={series} range="1h" format={String} height="fill" />);
    expect(screen.getByTestId("chart-CPU")).toHaveClass("h-full");
    expect(container.querySelector("[data-chart]")?.parentElement).toHaveClass("flex-1");
  });

  it("fills areas with a fading gradient and dots the newest point", () => {
    const { container } = render(
      <MetricChartCard title="CPU" rows={[...rows2, { t: 120_000, cpu: null }]} series={series} range="1h" format={String} />,
    );
    const [path] = areaPaths(container);
    const id = path.getAttribute("fill")?.match(/^url\(#(.+)\)$/)?.[1];
    expect(id).toBeTruthy();
    const stops = container.querySelectorAll(`linearGradient[id="${id}"] stop`);
    expect([...stops].map((s) => s.getAttribute("stop-opacity"))).toEqual(["0.32", "0.02"]);
    expect(container.querySelector("path.recharts-area-curve")).toHaveAttribute("stroke-width", "2");
    // One dot per series, on the last row that has a value.
    const dots = container.querySelectorAll("[data-last-dot='cpu']");
    expect(dots).toHaveLength(1);
  });

  it("puts round ticks on the value axis", () => {
    const { container } = render(
      <MetricChartCard title="CPU" rows={[{ t: 0, cpu: 12 }, { t: 60_000, cpu: 97 }]} series={series} range="1h" format={formatPercent} />,
    );
    const ticks = [...container.querySelectorAll(".recharts-yAxis .recharts-cartesian-axis-tick-value")].map((t) => t.textContent);
    expect(ticks).toEqual(["0%", "25%", "50%", "75%", "100%"]);
  });

  it("labels a stacked chart even with a single series", () => {
    render(
      <MetricChartCard title="CPU by app" rows={rows2} series={[{ key: "cpu", label: "Nextcloud", color: chartColor(0) }]} range="1h" format={String} stacked />,
    );
    expect(screen.getByTestId("chart-legend")).toHaveTextContent("Nextcloud");
  });

  it("hides the legend of a single unstacked series", () => {
    render(<MetricChartCard title="CPU" rows={rows2} series={series} range="1h" format={String} />);
    expect(screen.queryByTestId("chart-legend")).toBeNull();
  });

  it("wraps a long legend inside an overflow-safe card", () => {
    const many = Array.from({ length: 9 }, (_, i) => ({ key: `temp${i}`, label: `Package id ${i}`, color: chartColor(i) }));
    render(
      <MetricChartCard title="Temperature" rows={[{ t: 0, temp0: 1 }, { t: 60_000, temp0: 2 }]} series={many} range="1h" format={String} lines />,
    );
    expect(screen.getByTestId("chart-Temperature")).toHaveClass("min-w-0");
    const legend = screen.getByTestId("chart-legend");
    expect(legend).toHaveClass("flex-wrap");
    expect(legend.className).toContain("whitespace-nowrap");
    expect(legend.children).toHaveLength(9);
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

  it("fades its fill and dots the newest point", () => {
    const { container } = render(
      <Sparkline rows={[{ t: 0, cpu: 1 }, { t: 60_000, cpu: 2 }, { t: 120_000, cpu: 3 }]} field="cpu" color={chartColor(0)} />,
    );
    expect(areaPaths(container)[0].getAttribute("fill")).toMatch(/^url\(#spark/);
    expect(container.querySelectorAll("linearGradient stop")).toHaveLength(2);
    expect(container.querySelectorAll("[data-last-dot='cpu']")).toHaveLength(1);
  });

  it("renders nothing without enough numeric points", () => {
    const { container, rerender } = render(<Sparkline rows={[]} field="cpu" color={chartColor(0)} />);
    expect(container).toBeEmptyDOMElement();
    rerender(<Sparkline rows={[{ t: 0, cpu: 1 }, { t: 60_000, cpu: null }]} field="cpu" color={chartColor(0)} />);
    expect(container).toBeEmptyDOMElement();
  });
});

describe("ChartCardSkeleton", () => {
  it("is a frosted, chart-shaped loading card", () => {
    const { container } = render(<ChartCardSkeleton title="CPU" height={240} />);
    const card = screen.getByRole("status", { name: "Loading CPU" });
    expect(card).toHaveAttribute("data-slot", "widget-card");
    expect(container.querySelectorAll(".frost").length).toBeGreaterThan(4);
    expect(screen.queryByText(/Loading/)).toBeNull();
  });
});

describe("MetricTooltip", () => {
  const tipSeries = [
    { key: "a", label: "Nextcloud", color: "red" },
    { key: "b", label: "Gitea", color: "blue" },
  ];
  const payload = [
    { dataKey: "a", value: 1024, payload: { t: Date.UTC(2026, 9, 11, 17, 6) } },
    { dataKey: "b", value: 2048, payload: { t: Date.UTC(2026, 9, 11, 17, 6) } },
  ];

  it("renders nothing while inactive", () => {
    const { container } = render(<MetricTooltip active={false} payload={payload} series={tipSeries} format={formatSize} />);
    expect(container).toBeEmptyDOMElement();
  });

  it("lists label-left value-right rows with colour dots and a stack total", () => {
    render(<MetricTooltip active payload={payload} series={tipSeries} format={formatSize} total />);
    const tip = screen.getByTestId("chart-tooltip");
    expect(tip).toHaveClass("bg-popover/95", "backdrop-blur-sm");
    expect(tip).toHaveTextContent("Oct 11");
    expect(tip).toHaveTextContent("Nextcloud1.0 KB");
    expect(tip).toHaveTextContent("Gitea2.0 KB");
    expect(tip).toHaveTextContent("Total3.0 KB");
    expect(tip.querySelectorAll("span.rounded-full")).toHaveLength(2);
  });

  it("has no total row unless asked", () => {
    render(<MetricTooltip active payload={payload} series={tipSeries} format={formatSize} />);
    expect(screen.getByTestId("chart-tooltip")).not.toHaveTextContent("Total");
  });
});
