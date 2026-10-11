import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { MetricChartCard, ModeToggle, RangeSelect } from "@/components/metric-chart";
import { Sparkline } from "@/components/sparkline";
import { chartColor } from "@/lib/metrics";

const series = [{ key: "cpu", label: "CPU", color: chartColor(0) }];

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

  it("cycles theme colors", () => {
    expect(chartColor(0)).toBe("var(--chart-1)");
    expect(chartColor(5)).toBe("var(--chart-1)");
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
