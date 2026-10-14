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
