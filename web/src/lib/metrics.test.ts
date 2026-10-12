import { describe, expect, it } from "vitest";

import {
  chartColor,
  dockerDiskUsage,
  fieldKey,
  isUnavailable,
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
  temperatureChart,
  type MetricPoint,
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

describe("temperatureChart", () => {
  it("keys series by CSS-safe index and labels them with the sensor names", () => {
    const points: MetricPoint[] = [
      { t: 0, "temp:Core 0": 40, "temp:Core 0_max": 45, "temp:nvme: Composite": 30 },
      { t: 60, "temp:Core 0": 41, "temp:nvme: Composite": 31 },
    ];
    const { rows, series } = temperatureChart(points, 60, "avg");
    expect(series.map((s) => [s.key, s.label])).toEqual([
      ["temp0", "Core 0"],
      ["temp1", "nvme: Composite"],
    ]);
    for (const s of series) expect(s.key).toMatch(/^[a-z0-9]+$/);
    expect(rows).toEqual([
      { t: 0, temp0: 40, temp1: 30 },
      { t: 60000, temp0: 41, temp1: 31 },
    ]);
    expect(temperatureChart(points, 60, "peak").rows[0]).toEqual({ t: 0, temp0: 45, temp1: null });
  });
});

describe("chartColor", () => {
  it("starts with the theme colors and keeps 12 series distinct", () => {
    expect(chartColor(0)).toBe("var(--chart-1)");
    expect(chartColor(4)).toBe("var(--chart-5)");
    const colors = Array.from({ length: 12 }, (_, i) => chartColor(i));
    expect(new Set(colors).size).toBe(12);
  });
});

describe("isUnavailable", () => {
  it("needs the flag on at least half the bucket", () => {
    expect(isUnavailable(undefined)).toBe(false);
    expect(isUnavailable({ t: 0 })).toBe(false);
    expect(isUnavailable({ t: 0, unavailable: 0.05, unavailable_max: 1 })).toBe(false);
    expect(isUnavailable({ t: 0, unavailable: 0.5 })).toBe(true);
    expect(isUnavailable({ t: 0, unavailable: 1 })).toBe(true);
  });
});

describe("dockerDiskUsage", () => {
  const df = { images: 1, volumes: 2, build_cache: 3, reclaimable: 4 };
  it("prefers the latest sample over the range's points", () => {
    expect(dockerDiskUsage(df, [{ t: 0, docker_images: 9 }])).toEqual(df);
  });
  it("falls back to the newest point that carries a scan", () => {
    const points: MetricPoint[] = [
      { t: 0, docker_images: 5, docker_volumes: 6, docker_build_cache: 7, docker_reclaimable: 8 },
      { t: 60, cpu: 1 },
    ];
    expect(dockerDiskUsage(undefined, points)).toEqual({ images: 5, volumes: 6, build_cache: 7, reclaimable: 8 });
    expect(dockerDiskUsage(undefined, [{ t: 0, cpu: 1 }])).toBeUndefined();
  });
});
