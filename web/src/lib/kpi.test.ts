import { describe, expect, it } from "vitest";

import {
  appLabel,
  deltaLabel,
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

  it("compares the newer half of the range with the older half", () => {
    expect(periodDelta(rows([10, 10, 20, 20]), "cpu")).toBe(100);
    expect(periodDelta(rows([20, 20, 10, 10]), "cpu")).toBe(-50);
    // Too few points or a zero baseline: no pill.
    expect(periodDelta(rows([10, 20]), "cpu")).toBeNull();
    expect(periodDelta(rows([0, 0, 5, 5]), "cpu")).toBeNull();
    expect(deltaLabel("1h")).toBe("vs previous 30 min");
    expect(deltaLabel("7d")).toBe("vs previous 3.5 days");
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
