// Monitoring data model and pure helpers shared by the dashboard widgets,
// the server Monitoring page and the app Metrics tab. Field names mirror
// the Go flattening in internal/domain/metrics (ServerSample.Values,
// AppSample.Values); every field has a "<field>_max" peak on chart points.

export type MetricsRange = "1h" | "12h" | "24h" | "7d" | "30d";

export const METRICS_RANGES: { value: MetricsRange; label: string }[] = [
  { value: "1h", label: "1 hour" },
  { value: "12h", label: "12 hours" },
  { value: "24h", label: "24 hours" },
  { value: "7d", label: "7 days" },
  { value: "30d", label: "30 days" },
];

// "avg" plots each point's average, "peak" its "<field>_max".
export type MetricsMode = "avg" | "peak";

// A chart point: "t" (unix seconds) plus field averages and peaks.
export type MetricPoint = Record<string, number>;

export type Series = { interval_seconds: number; points: MetricPoint[] };

export type AppSeries = { app_id: string; points: MetricPoint[] };

export type ServerSeries = Series & { apps: AppSeries[] };

export type Disk = { mount: string; used: number; total: number };

export type DockerDF = {
  images: number;
  volumes: number;
  build_cache: number;
  reclaimable: number;
};

// ServerSample mirrors metrics.ServerSample (the latest host sample).
export type ServerSample = {
  cpu?: number;
  mem_used: number;
  mem_total: number;
  swap_used: number;
  swap_total: number;
  load1: number;
  load5: number;
  load15: number;
  disks: Disk[] | null;
  disk_read_bps?: number;
  disk_write_bps?: number;
  net_rx_bps?: number;
  net_tx_bps?: number;
  temp?: number;
  temps?: Record<string, number>;
  docker_df?: DockerDF;
};

export type AppCounts = { running: number; stopped: number; problematic: number };

export type ServerOverview = {
  server_id: string;
  liveness: "online" | "unknown";
  latest: { at: string; server: ServerSample } | null;
  sparkline: MetricPoint[];
  apps: AppCounts;
};

// TEMP_PREFIX prefixes one sensor's field ("temp:Composite").
export const TEMP_PREFIX = "temp:";

// fieldKey is the point key a mode reads for field.
export function fieldKey(field: string, mode: MetricsMode): string {
  return mode === "peak" ? `${field}_max` : field;
}

// ChartRow is a point flattened for recharts: "t" in milliseconds, values
// keyed by field; null breaks the line.
export type ChartRow = { t: number } & Record<string, number | null>;

// toChartRows maps points to recharts rows for the given fields and mode,
// inserting a null row inside every gap longer than 1.5 intervals so the
// lines break where the target did not report instead of bridging it.
export function toChartRows(
  points: MetricPoint[],
  intervalSeconds: number,
  fields: string[],
  mode: MetricsMode,
): ChartRow[] {
  const rows: ChartRow[] = [];
  let prev: number | null = null;
  for (const p of points) {
    const t = p.t;
    if (prev !== null && intervalSeconds > 0 && t - prev > intervalSeconds * 1.5) {
      const gap: ChartRow = { t: (prev + intervalSeconds) * 1000 };
      for (const f of fields) gap[f] = null;
      rows.push(gap);
    }
    const row: ChartRow = { t: t * 1000 };
    for (const f of fields) {
      const v = p[fieldKey(f, mode)];
      row[f] = typeof v === "number" ? v : null;
    }
    rows.push(row);
    prev = t;
  }
  return rows;
}

// hasData reports whether any point carries any of the fields.
export function hasData(points: MetricPoint[], fields: string[]): boolean {
  return points.some((p) => fields.some((f) => typeof p[f] === "number"));
}

// sensorFields lists the per-sensor temperature fields present in points.
export function sensorFields(points: MetricPoint[]): string[] {
  const out = new Set<string>();
  for (const p of points) {
    for (const k of Object.keys(p)) {
      if (k.startsWith(TEMP_PREFIX) && !k.endsWith("_max")) out.add(k);
    }
  }
  return [...out].sort();
}

// stackApps turns per-app series into rows keyed by app id for one field,
// aligned on t (an app without a point at t contributes nothing there).
export function stackApps(
  apps: AppSeries[],
  field: string,
  mode: MetricsMode,
): ChartRow[] {
  const byT = new Map<number, ChartRow>();
  for (const a of apps) {
    for (const p of a.points) {
      const v = p[fieldKey(field, mode)];
      if (typeof v !== "number") continue;
      const row = byT.get(p.t) ?? { t: p.t * 1000 };
      row[a.app_id] = v;
      byT.set(p.t, row);
    }
  }
  return [...byT.values()].sort((x, y) => x.t - y.t);
}

// lastPoint is the newest point, or undefined.
export function lastPoint(points: MetricPoint[]): MetricPoint | undefined {
  return points.length ? points[points.length - 1] : undefined;
}

const UNITS = ["B", "KB", "MB", "GB", "TB", "PB"];

// formatSize renders bytes with a binary unit and one decimal ("1.5 GB").
export function formatSize(bytes: number | null | undefined): string {
  if (bytes === null || bytes === undefined || !Number.isFinite(bytes)) return "–";
  let v = Math.max(0, bytes);
  let i = 0;
  while (v >= 1024 && i < UNITS.length - 1) {
    v /= 1024;
    i++;
  }
  return `${i === 0 ? v.toFixed(0) : v.toFixed(1)} ${UNITS[i]}`;
}

// formatRate renders bytes per second ("1.2 MB/s").
export function formatRate(bps: number | null | undefined): string {
  const s = formatSize(bps);
  return s === "–" ? s : `${s}/s`;
}

// formatPercent renders a percentage with one decimal under 10 %.
export function formatPercent(v: number | null | undefined): string {
  if (v === null || v === undefined || !Number.isFinite(v)) return "–";
  return `${v < 10 ? v.toFixed(1) : Math.round(v)}%`;
}

// formatTemp renders °C.
export function formatTemp(v: number | null | undefined): string {
  if (v === null || v === undefined || !Number.isFinite(v)) return "–";
  return `${Math.round(v)}°C`;
}

// percentOf is used/total in percent, undefined without a total.
export function percentOf(used: number | undefined, total: number | undefined): number | undefined {
  if (used === undefined || !total) return undefined;
  return (100 * used) / total;
}

// fullestDisk is the filesystem with the highest usage percentage.
export function fullestDisk(disks: Disk[] | null | undefined): Disk | undefined {
  let best: Disk | undefined;
  for (const d of disks ?? []) {
    if (!d.total) continue;
    if (!best || d.used / d.total > best.used / best.total) best = d;
  }
  return best;
}

// formatTick renders an x-axis tick for the range (time of day for short
// ranges, date for long ones).
export function formatTick(ms: number, range: MetricsRange): string {
  const d = new Date(ms);
  if (range === "7d" || range === "30d") {
    return d.toLocaleDateString(undefined, { month: "short", day: "numeric" });
  }
  return d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });
}

const CHART_COLORS = ["var(--chart-1)", "var(--chart-2)", "var(--chart-3)", "var(--chart-4)", "var(--chart-5)"];

// chartColor cycles the theme's five chart colors.
export function chartColor(i: number): string {
  return CHART_COLORS[i % CHART_COLORS.length];
}
