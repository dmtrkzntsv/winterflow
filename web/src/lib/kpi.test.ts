import { describe, expect, it } from "vitest";

import {
  appLabel,
  kpiNow,
  formatAge,
  lastSeenText,
  latestValue,
  percentCeiling,
  withNetTotal,
} from "@/lib/kpi";
import type { ChartRow } from "@/lib/metrics";

const rows = (vs: (number | null)[]): ChartRow[] => vs.map((v, i) => ({ t: i * 60_000, cpu: v }));

describe("kpi helpers", () => {
  it("takes the newest value past trailing gaps", () => {
    expect(latestValue(rows([1, 2, null]), "cpu")).toBe(2);
    expect(latestValue(rows([null, null]), "cpu")).toBeUndefined();
    expect(latestValue([], "cpu")).toBeUndefined();
  });

  it("takes headline figures from the live sample, rows as fallback", () => {
    const latest = {
      cpu: 12,
      mem_used: 4,
      mem_total: 16,
      swap_used: 0,
      swap_total: 0,
      load1: 0.5,
      load5: 0,
      load15: 0,
      disks: [{ mount: "/", used: 30, total: 100 }],
      net_rx_bps: 100,
      net_tx_bps: 50,
    };
    const fallback = { cpu: 99, mem_pct: 99, disk_pct: 99, net_bps: 99, net_rx_bps: 9, net_tx_bps: 9, load1: 9 };
    expect(kpiNow(latest, fallback)).toEqual({
      cpu: 12, mem_pct: 25, disk_pct: 30, net_bps: 150, net_rx_bps: 100, net_tx_bps: 50, load1: 0.5,
    });
    expect(kpiNow(undefined, fallback)).toEqual(fallback);
    // A first sample after a restart has no CPU or rates yet.
    const partial = { ...latest, cpu: undefined, net_rx_bps: undefined, net_tx_bps: undefined, disks: null };
    expect(kpiNow(partial, fallback)).toMatchObject({ cpu: 99, net_bps: 99, disk_pct: 99, mem_pct: 25 });
  });

  it("sums network in and out, peaks included", () => {
    const [a, b] = withNetTotal([
      { t: 0, net_rx_bps: 1, net_tx_bps: 2, net_rx_bps_max: 5 },
      { t: 60, cpu: 1 },
    ]);
    expect(a.net_bps).toBe(3);
    expect(a.net_bps_max).toBe(7);
    expect(b.net_bps).toBeUndefined();
  });

  it("tops a percentage sparkline at a round step", () => {
    expect(percentCeiling(rows([1, 3]), "cpu")).toBe(10);
    expect(percentCeiling(rows([10, 37]), "cpu")).toBe(40);
    expect(percentCeiling(rows([90, 99]), "cpu")).toBe(100);
    expect(percentCeiling(rows([]), "cpu")).toBe(10);
  });

  it("renders ages compactly", () => {
    expect(formatAge(30_000)).toBe("just now");
    expect(formatAge(5 * 60_000)).toBe("5m");
    expect(formatAge(3 * 3600_000)).toBe("3h");
    expect(formatAge(72 * 3600_000)).toBe("3d");
    expect(formatAge(-5)).toBe("just now");
  });

  it("names a nameless app Unnamed app, never by its id", () => {
    expect(appLabel("Nextcloud")).toBe("Nextcloud");
    expect(appLabel("  ")).toBe("Unnamed app");
    expect(appLabel(undefined)).toBe("Unnamed app");
  });
});

describe("lastSeenText", () => {
  const now = Date.parse("2026-10-11T12:05:00Z");

  it("names the age from the timestamp, and 'just now' under a minute", () => {
    expect(lastSeenText("2026-10-11T12:00:00Z", now)).toBe("last seen 5m ago");
    expect(lastSeenText("2026-10-11T12:04:50Z", now)).toBe("last seen just now");
    expect(lastSeenText("2026-10-08T12:05:00Z", now)).toBe("last seen 3d ago");
  });

  it("is empty for a server that never connected", () => {
    expect(lastSeenText(null, now)).toBe("");
    expect(lastSeenText("not a date", now)).toBe("");
  });
});
