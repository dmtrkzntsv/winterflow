import { describe, expect, it } from "vitest";

import {
  fieldKey,
  formatPercent,
  formatRate,
  formatSize,
  formatTemp,
  fullestDisk,
  hasData,
  lastPoint,
  percentOf,
  sensorFields,
  stackApps,
  toChartRows,
} from "@/lib/metrics";

describe("toChartRows", () => {
  it("reads averages or peaks and converts t to milliseconds", () => {
    const pts = [{ t: 60, cpu: 10, cpu_max: 40 }, { t: 120, cpu: 20, cpu_max: 25 }];
    expect(toChartRows(pts, 60, ["cpu"], "avg")).toEqual([
      { t: 60_000, cpu: 10 },
      { t: 120_000, cpu: 20 },
    ]);
    expect(toChartRows(pts, 60, ["cpu"], "peak").map((r) => r.cpu)).toEqual([40, 25]);
  });

  it("breaks the line over a gap (server offline) with a null row", () => {
    const pts = [{ t: 0, cpu: 1 }, { t: 60, cpu: 2 }, { t: 600, cpu: 3 }];
    const rows = toChartRows(pts, 60, ["cpu"], "avg");
    expect(rows).toHaveLength(4);
    expect(rows[2]).toEqual({ t: 120_000, cpu: null });
    // A slightly late sample is not a gap.
    expect(toChartRows([{ t: 0, cpu: 1 }, { t: 85, cpu: 2 }], 60, ["cpu"], "avg")).toHaveLength(2);
  });

  it("maps missing fields (first sample has no rates) to null", () => {
    expect(toChartRows([{ t: 0, mem_used: 5 }], 60, ["cpu"], "avg")).toEqual([{ t: 0, cpu: null }]);
  });
});

describe("helpers", () => {
  it("fieldKey", () => {
    expect(fieldKey("cpu", "avg")).toBe("cpu");
    expect(fieldKey("cpu", "peak")).toBe("cpu_max");
  });

  it("hasData and lastPoint", () => {
    expect(hasData([], ["cpu"])).toBe(false);
    expect(hasData([{ t: 1, mem_used: 1 }], ["cpu"])).toBe(false);
    expect(hasData([{ t: 1, cpu: 0 }], ["cpu"])).toBe(true);
    expect(lastPoint([])).toBeUndefined();
    expect(lastPoint([{ t: 1 }, { t: 2 }])).toEqual({ t: 2 });
  });

  it("sensorFields lists sensors without peaks, sorted", () => {
    expect(
      sensorFields([{ t: 1, "temp:b": 1, "temp:b_max": 2, temp: 3 }, { t: 2, "temp:a": 1 }]),
    ).toEqual(["temp:a", "temp:b"]);
  });

  it("stackApps aligns apps on t", () => {
    const rows = stackApps(
      [
        { app_id: "a", points: [{ t: 60, cpu: 1, cpu_max: 2 }, { t: 0, cpu: 3 }] },
        { app_id: "b", points: [{ t: 60, cpu: 4 }, { t: 120, mem_used: 9 }] },
      ],
      "cpu",
      "avg",
    );
    expect(rows).toEqual([
      { t: 0, a: 3 },
      { t: 60_000, a: 1, b: 4 },
    ]);
  });

  it("formats sizes, rates, percents and temperatures", () => {
    expect(formatSize(0)).toBe("0 B");
    expect(formatSize(1536)).toBe("1.5 KB");
    expect(formatSize(5 * 1024 ** 3)).toBe("5.0 GB");
    expect(formatSize(undefined)).toBe("–");
    expect(formatRate(2048)).toBe("2.0 KB/s");
    expect(formatRate(undefined)).toBe("–");
    expect(formatPercent(4.25)).toBe("4.3%");
    expect(formatPercent(93.6)).toBe("94%");
    expect(formatPercent(Number.NaN)).toBe("–");
    expect(formatTemp(41.6)).toBe("42°C");
    expect(formatTemp(undefined)).toBe("–");
  });

  it("percentOf and fullestDisk", () => {
    expect(percentOf(1, 4)).toBe(25);
    expect(percentOf(1, 0)).toBeUndefined();
    expect(percentOf(undefined, 4)).toBeUndefined();
    const disks = [
      { mount: "/", used: 10, total: 100 },
      { mount: "/data", used: 90, total: 100 },
      { mount: "/empty", used: 0, total: 0 },
    ];
    expect(fullestDisk(disks)?.mount).toBe("/data");
    expect(fullestDisk(null)).toBeUndefined();
  });
});
