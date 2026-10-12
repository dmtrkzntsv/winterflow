import { cloneElement, type ReactElement } from "react";
import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { KpiTile, KpiTileSkeleton } from "@/components/kpi-tile";
import { chartColor } from "@/lib/metrics";

// jsdom has no layout: a fixed-size ResponsiveContainer makes recharts draw.
vi.mock("recharts", async (importOriginal) => {
  const mod = await importOriginal<typeof import("recharts")>();
  return {
    ...mod,
    ResponsiveContainer: ({ children }: { children: ReactElement<{ width?: number; height?: number }> }) => (
      <div style={{ width: 200, height: 40 }}>{cloneElement(children, { width: 200, height: 40 })}</div>
    ),
  };
});

const rows = [
  { t: 0, cpu: 10 },
  { t: 60_000, cpu: 30 },
  { t: 120_000, cpu: 20 },
];

describe("KpiTile", () => {
  it("shows a muted label over a large proportional figure", () => {
    render(<KpiTile label="CPU" value="42%" sub="of host" />);
    const tile = screen.getByTestId("kpi-CPU");
    expect(tile).toHaveAttribute("data-slot", "widget-card");
    expect(screen.getByRole("heading", { name: "CPU" })).toBeInTheDocument();
    expect(screen.getByText("42%")).toBeInTheDocument();
    expect(tile).toHaveTextContent("of host");
    expect(screen.queryByTestId("kpi-delta")).toBeNull();
  });

  it("paints a rise green and a fall red by default", () => {
    const { rerender } = render(<KpiTile label="Visitors" value="925" delta={3.6} />);
    expect(screen.getByTestId("kpi-delta")).toHaveTextContent("+3.6%vs previous");
    expect(screen.getByText("+3.6%")).toHaveAttribute("data-tone", "good");
    rerender(<KpiTile label="Visitors" value="925" delta={-12} deltaLabel="vs last week" />);
    expect(screen.getByText("-12%")).toHaveAttribute("data-tone", "bad");
    expect(screen.getByTestId("kpi-delta")).toHaveTextContent("vs last week");
  });

  it("inverts or mutes the tone on request and hides a null delta", () => {
    const { rerender } = render(<KpiTile label="CPU" value="42%" delta={20} goodWhen="down" />);
    expect(screen.getByText("+20%")).toHaveAttribute("data-tone", "bad");
    rerender(<KpiTile label="CPU" value="42%" delta={20} goodWhen="neutral" />);
    expect(screen.getByText("+20%")).toHaveAttribute("data-tone", "neutral");
    rerender(<KpiTile label="CPU" value="42%" delta={null} />);
    expect(screen.queryByTestId("kpi-delta")).toBeNull();
  });

  it("keeps a flat or trivial delta neutral, a flat one without an arrow", () => {
    const { rerender } = render(<KpiTile label="CPU" value="42%" delta={0.04} goodWhen="down" />);
    const flat = screen.getByText("0%");
    expect(flat).toHaveAttribute("data-tone", "neutral");
    expect(flat.querySelector("svg")).toBeNull();
    rerender(<KpiTile label="CPU" value="42%" delta={180} significant={false} goodWhen="down" />);
    const trivial = screen.getByText("+180%");
    expect(trivial).toHaveAttribute("data-tone", "neutral");
    expect(trivial.querySelector("svg")).not.toBeNull();
  });

  it("draws a sparkline with a dot on the newest point", () => {
    const { container } = render(
      <KpiTile label="CPU" value="20%" spark={{ rows, field: "cpu", color: chartColor(0), max: 100 }} />,
    );
    expect(screen.getByTestId("sparkline-cpu")).toBeInTheDocument();
    expect(container.querySelectorAll("[data-last-dot='cpu']")).toHaveLength(1);
  });
});

describe("KpiTileSkeleton", () => {
  it("is a frosted, tile-shaped placeholder without text", () => {
    const { container } = render(<KpiTileSkeleton />);
    expect(screen.getByRole("status", { name: "Loading" })).toHaveAttribute("data-slot", "widget-card");
    expect(container.querySelectorAll(".frost")).toHaveLength(3);
    render(<KpiTileSkeleton label="Memory" />);
    expect(screen.getByRole("status", { name: "Loading Memory" })).toHaveTextContent("Memory");
  });
});
