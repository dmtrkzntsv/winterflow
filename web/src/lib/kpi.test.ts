import { describe, expect, it } from "vitest";

import {
  appLabel,
  deltaDirection,
  deltaLabel,
  isSignificant,
  kpiNow,
  formatAge,
  latestValue,
  percentCeiling,
  periodDelta,
  serverPresence,
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

  it("compares the newer half of the range's time with the older half", () => {
    // 1h of one-minute rows: 30 at 10 %, 30 at 20 %.
    const full = rows([...Array(30).fill(10), ...Array(30).fill(20)]);
    expect(periodDelta(full, "cpu", "1h", 60)).toEqual({ percent: 100, change: 10 });
    expect(periodDelta(rows([...Array(30).fill(20), ...Array(30).fill(10)]), "cpu", "1h", 60)?.percent).toBe(-50);
    // Zero baseline: no ratio.
    expect(periodDelta(rows([...Array(30).fill(0), ...Array(30).fill(5)]), "cpu", "1h", 60)).toBeNull();
    expect(deltaLabel("1h")).toBe("vs previous 30 min");
    expect(deltaLabel("7d")).toBe("vs previous 3.5 days");
  });

  it("splits on time, not on row count", () => {
    // The older half is dense; the newer one only has its last 20 minutes.
    // A count split would cut at row 25 and mix 5 older rows into "current".
    const r: ChartRow[] = [
      ...Array.from({ length: 30 }, (_, i) => ({ t: i * 60_000, cpu: 10 })),
      ...Array.from({ length: 20 }, (_, i) => ({ t: (40 + i) * 60_000, cpu: 40 })),
    ];
    expect(periodDelta(r, "cpu", "1h", 60)?.percent).toBe(300);
  });

  it("hides the delta on a short history or a gappy half", () => {
    // A server that came up 10 minutes ago, viewed on 1h.
    expect(periodDelta(rows(Array(10).fill(10)), "cpu", "1h", 60)).toBeNull();
    // A young install on 30d: a couple of 8-hour buckets.
    const young: ChartRow[] = [0, 1, 2, 3, 4, 5].map((i) => ({ t: i * 8 * 3600_000, cpu: 10 + i }));
    expect(periodDelta(young, "cpu", "30d", 8 * 3600)).toBeNull();
    // An outage covering most of the older half.
    const gappy: ChartRow[] = [
      ...Array.from({ length: 5 }, (_, i) => ({ t: i * 60_000, cpu: 10 })),
      { t: 5 * 60_000, cpu: null },
      ...Array.from({ length: 30 }, (_, i) => ({ t: (30 + i) * 60_000, cpu: 20 })),
    ];
    expect(periodDelta(gappy, "cpu", "1h", 60)).toBeNull();
    // Half coverage in each half is enough.
    const half: ChartRow[] = [
      ...Array.from({ length: 15 }, (_, i) => ({ t: (15 + i) * 60_000, cpu: 10 })),
      ...Array.from({ length: 15 }, (_, i) => ({ t: (45 + i) * 60_000, cpu: 20 })),
    ];
    expect(periodDelta(half, "cpu", "1h", 60)?.percent).toBe(100);
    expect(periodDelta([], "cpu", "1h", 60)).toBeNull();
  });

  it("calls a rounding-to-zero delta flat", () => {
    expect(deltaDirection(0)).toBe("flat");
    expect(deltaDirection(0.04)).toBe("flat");
    expect(deltaDirection(-0.4)).toBe("flat");
    expect(deltaDirection(0.6)).toBe("up");
    expect(deltaDirection(-3)).toBe("down");
  });

  it("only colours meaningful moves", () => {
    // Idle CPU 0.5 % -> 1.4 %: +180 % but under one percentage point.
    expect(isSignificant({ percent: 180, change: 0.9 }, "percent")).toBe(false);
    expect(isSignificant({ percent: 30, change: 6 }, "percent")).toBe(true);
    // Amounts: under 2 % relative is noise.
    expect(isSignificant({ percent: 1.5, change: 1e6 }, "amount")).toBe(false);
    expect(isSignificant({ percent: 18, change: 1e6 }, "amount")).toBe(true);
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

  it("labels a server's presence", () => {
    const now = Date.parse("2026-10-11T12:05:00Z");
    expect(serverPresence(true, null, now)).toMatchObject({ tone: "live", label: "just now" });
    expect(serverPresence(false, "2026-10-11T12:00:00Z", now)).toMatchObject({ tone: "offline", label: "offline 5m" });
    expect(serverPresence(false, "2026-10-11T12:04:50Z", now).label).toBe("offline");
    expect(serverPresence(false, null, now)).toMatchObject({ tone: "waiting", label: "waiting" });
  });

  it("names a nameless app by its id prefix", () => {
    expect(appLabel("Nextcloud", "abc")).toBe("Nextcloud");
    expect(appLabel("", "0123456789")).toBe("01234567");
    expect(appLabel(undefined, "0123456789")).toBe("01234567");
  });
});
