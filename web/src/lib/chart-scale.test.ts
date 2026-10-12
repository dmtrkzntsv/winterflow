import { describe, expect, it } from "vitest";

import {
  average,
  axisWidth,
  deltaPercent,
  formatDelta,
  niceAxis,
  niceTicks,
  scaleFor,
  splitHalves,
  stackTotals,
  tooltipRows,
  valuesOf,
} from "@/lib/chart-scale";
import { formatPercent, formatRate, formatSize, formatTemp } from "@/lib/metrics";

const KB = 1024;
const MB = 1024 ** 2;

describe("niceTicks", () => {
  it("steps by 1/2/2.5/5 x 10^n from zero past the top", () => {
    expect(niceTicks([95, 190, 380])).toEqual([0, 100, 200, 300, 400]);
    expect(niceTicks([9])).toEqual([0, 2.5, 5, 7.5, 10]);
    expect(niceTicks([0.03])).toEqual([0, 0.01, 0.02, 0.03]);
  });

  it("covers negatives and gives a flat or empty series an axis", () => {
    expect(niceTicks([-30, 70])).toEqual([-50, -25, 0, 25, 50, 75]);
    expect(niceTicks([])).toEqual([0, 1]);
    expect(niceTicks([0, 0])).toEqual([0, 1]);
    expect(niceTicks([NaN, Infinity])).toEqual([0, 1]);
  });

  it("never lands on float noise", () => {
    for (const t of niceTicks([0.7])) expect(String(t).length).toBeLessThan(6);
  });
});

describe("niceAxis", () => {
  it("reads percent as quarters above 50 % and tighter below", () => {
    const high = niceAxis([12, 97], "percent");
    expect(high.ticks).toEqual([0, 25, 50, 75, 100]);
    expect(high.domain).toEqual([0, 100]);
    expect(high.ticks.map(high.label)).toEqual(["0%", "25%", "50%", "75%", "100%"]);
    expect(niceAxis([3.2], "percent").ticks).toEqual([0, 1, 2, 3, 4]);
    expect(niceAxis([42], "percent").ticks).toEqual([0, 20, 40, 60]);
  });

  it("gives an idle 0 % series a 0-1 % axis", () => {
    const a = niceAxis([0, 0, null], "percent");
    expect(a.ticks).toEqual([0, 0.5, 1]);
    expect(a.ticks.map(a.label)).toEqual(["0%", "0.5%", "1%"]);
  });

  it("steps bytes by 1/2/5 x 10^n in one binary unit", () => {
    const a = niceAxis([98 * KB, 391 * KB], "bytes");
    expect(a.ticks.map(a.label)).toEqual(["0 KB", "100 KB", "200 KB", "300 KB", "400 KB"]);
    expect(a.domain[1]).toBe(400 * KB);
    const small = niceAxis([1.5 * KB], "bytes");
    expect(small.ticks.map(small.label)).toEqual(["0 KB", "0.5 KB", "1 KB", "1.5 KB"]);
    expect(niceAxis([700], "bytes").ticks.map(niceAxis([700], "bytes").label)).toEqual([
      "0 B",
      "200 B",
      "400 B",
      "600 B",
      "800 B",
    ]);
  });

  it("labels rates per second", () => {
    const a = niceAxis([57 * MB], "rate");
    expect(a.ticks.map(a.label)).toEqual(["0 MB/s", "20 MB/s", "40 MB/s", "60 MB/s"]);
    expect(niceAxis([], "rate").ticks).toEqual([0, 1]);
    // Never "1000 KB/s": one unit up instead.
    const near = niceAxis([900 * KB], "rate");
    expect(near.ticks.map(near.label)).toEqual(["0 MB/s", "0.5 MB/s", "1 MB/s"]);
  });

  it("keeps load on whole numbers", () => {
    expect(niceAxis([0.3, 0.8], "integer").ticks).toEqual([0, 1]);
    expect(niceAxis([2.4], "integer").ticks).toEqual([0, 1, 2, 3]);
    const big = niceAxis([15.2], "integer");
    expect(big.ticks).toEqual([0, 5, 10, 15, 20]);
    expect(big.ticks.map(big.label)).toEqual(["0", "5", "10", "15", "20"]);
  });

  it("labels a plain number with the chart's formatter", () => {
    const a = niceAxis([78], "number", formatTemp);
    expect(a.ticks).toEqual([0, 20, 40, 60, 80]);
    expect(a.label(40)).toBe("40°C");
  });
});

describe("scaleFor and axisWidth", () => {
  it("infers the scale from the value formatter", () => {
    expect(scaleFor(formatPercent)).toBe("percent");
    expect(scaleFor(formatSize)).toBe("bytes");
    expect(scaleFor(formatRate)).toBe("rate");
    expect(scaleFor(formatTemp)).toBe("number");
  });

  it("sizes the axis to its widest label, compactly", () => {
    expect(axisWidth(["0%", "100%"])).toBeLessThanOrEqual(46);
    expect(axisWidth(["0 B", "400 KB/s"])).toBeGreaterThan(axisWidth(["0%", "100%"]));
    expect(axisWidth([])).toBeGreaterThan(0);
  });
});

describe("stackTotals and valuesOf", () => {
  const rows = [
    { t: 0, a: 1, b: 2 },
    { t: 1, a: null, b: 4 },
  ];
  it("sums a stack per row and lists every numeric value", () => {
    expect(stackTotals(rows, ["a", "b"])).toEqual([3, 4]);
    expect(valuesOf(rows, ["a", "b"])).toEqual([1, 2, 4]);
  });
});

describe("deltaPercent", () => {
  it("compares the period averages", () => {
    expect(average([1, null, 3])).toBe(2);
    expect(average([null])).toBeNull();
    expect(deltaPercent([11, 13], [10, 10])).toBeCloseTo(20);
    expect(deltaPercent([5, 5], [10, 10])).toBeCloseTo(-50);
  });

  it("is null without enough data or a zero baseline", () => {
    expect(deltaPercent([1], [1, 2])).toBeNull();
    expect(deltaPercent([1, 2], [null, 3])).toBeNull();
    expect(deltaPercent([1, 2], [0, 0])).toBeNull();
    expect(deltaPercent([1], [2], 1)).toBeCloseTo(-50);
  });

  it("splits one range into its older and newer half", () => {
    expect(splitHalves([1, 2, 3, 4, 5])).toEqual({ previous: [1, 2], current: [4, 5] });
    expect(splitHalves([])).toEqual({ previous: [], current: [] });
  });

  it("formats the pill figure", () => {
    expect(formatDelta(12.06)).toBe("+12.1%");
    expect(formatDelta(-4)).toBe("-4%");
    expect(formatDelta(-38.64)).toBe("-38.6%");
    expect(formatDelta(129.6)).toBe("+130%");
    expect(formatDelta(0.01)).toBe("0%");
  });
});

describe("tooltipRows", () => {
  const series = [
    { key: "a", label: "Nextcloud", color: "red" },
    { key: "b", label: "Gitea", color: "blue" },
  ];
  it("lists series in series order with formatted values", () => {
    const out = tooltipRows([{ dataKey: "b", value: 2 }, { dataKey: "a", value: 1.25 }], series, formatPercent);
    expect(out.rows.map((r) => [r.label, r.value, r.color])).toEqual([
      ["Nextcloud", "1.3%", "red"],
      ["Gitea", "2.0%", "blue"],
    ]);
    expect(out.total).toBeUndefined();
  });

  it("adds a total for a stack of two or more and skips missing values", () => {
    expect(tooltipRows([{ dataKey: "a", value: 1 }, { dataKey: "b", value: 2 }], series, String, true).total).toBe("3");
    const one = tooltipRows([{ dataKey: "a", value: 1 }, { dataKey: "b", value: null }], series, String, true);
    expect(one.rows).toHaveLength(1);
    expect(one.total).toBeUndefined();
  });
});
