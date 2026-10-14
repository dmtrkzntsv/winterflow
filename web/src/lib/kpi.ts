// Pure helpers behind the KPI rows, the dashboard's server cards and the
// freshness line: the newest value of a series, its delta within the
// fetched range, relative ages and presence labels. No DOM, so they test
// in isolation.

import { average, niceTicks } from "@/lib/chart-scale";
import { fullestDisk, percentOf, type ChartRow, type MetricPoint, type MetricsRange, type ServerSample } from "@/lib/metrics";

// latestValue is the newest numeric value of field, skipping the null gap
// rows toChartRows inserts.
export function latestValue(rows: ChartRow[], field: string): number | undefined {
  for (let i = rows.length - 1; i >= 0; i--) {
    const v = rows[i][field];
    if (typeof v === "number" && Number.isFinite(v)) return v;
  }
  return undefined;
}

const RANGE_MS: Record<MetricsRange, number> = {
  "1h": 3600_000,
  "12h": 12 * 3600_000,
  "24h": 24 * 3600_000,
  "7d": 7 * 24 * 3600_000,
  "30d": 30 * 24 * 3600_000,
};

// KpiDelta is a period-over-period change: percent relative to the
// previous half's average, and change, the plain difference of the two
// averages (percentage points for a percent metric).
export type KpiDelta = { percent: number; change: number };

// periodDelta compares the newer half of the range with the older half,
// split at the range's time midpoint (ending with the newest bucket), so
// the "vs previous 30 min" label is true. Each half must hold at least half
// the samples its span should (intervalSeconds apart) and two at least:
// a young install, a restart or an outage hides the pill instead of
// comparing ten minutes with five. null too on a zero baseline.
export function periodDelta(
  rows: ChartRow[],
  field: string,
  range: MetricsRange,
  intervalSeconds: number,
): KpiDelta | null {
  if (rows.length === 0 || !(intervalSeconds > 0)) return null;
  const step = intervalSeconds * 1000;
  const end = rows[rows.length - 1].t + step;
  const half = RANGE_MS[range] / 2;
  const mid = end - half;
  const previous: number[] = [];
  const current: number[] = [];
  for (const r of rows) {
    const v = r[field];
    if (typeof v !== "number" || !Number.isFinite(v) || r.t < end - 2 * half) continue;
    (r.t < mid ? previous : current).push(v);
  }
  const needed = Math.max(2, Math.ceil((0.5 * half) / step));
  if (previous.length < needed || current.length < needed) return null;
  const prev = average(previous);
  const cur = average(current);
  if (prev === null || cur === null || prev === 0) return null;
  return { percent: (100 * (cur - prev)) / Math.abs(prev), change: cur - prev };
}

// deltaDirection is where a delta points; one that shows as 0 % is flat
// (no arrow, no colour).
export function deltaDirection(percent: number): "up" | "down" | "flat" {
  if (Math.abs(percent) < 0.5) return "flat";
  return percent > 0 ? "up" : "down";
}

// isSignificant tells a move worth colouring from noise: at least one
// percentage point for a percent metric (idle CPU 0.5 % -> 1.4 % is +180 %
// but nothing happened), at least 2 % relative for an amount.
export function isSignificant(delta: KpiDelta, unit: "percent" | "amount"): boolean {
  if (unit === "percent") return Math.abs(delta.change) >= 1;
  return Math.abs(delta.percent) >= 2;
}

export type KpiNow = {
  cpu?: number;
  mem_pct?: number;
  disk_pct?: number;
  net_bps?: number;
  net_rx_bps?: number;
  net_tx_bps?: number;
  load1?: number;
};

// kpiNow is the monitoring KPI row's headline figures: the live sample
// (current on every range), each figure falling back to the range's newest
// value when the sample lacks it. 7d/30d rows are hours-old rollups, so
// they only stand in.
export function kpiNow(latest: ServerSample | undefined, fallback: KpiNow): KpiNow {
  if (!latest) return fallback;
  const disk = fullestDisk(latest.disks);
  const hasNet = typeof latest.net_rx_bps === "number" || typeof latest.net_tx_bps === "number";
  return {
    cpu: latest.cpu ?? fallback.cpu,
    mem_pct: percentOf(latest.mem_used, latest.mem_total) ?? fallback.mem_pct,
    disk_pct: (disk ? percentOf(disk.used, disk.total) : undefined) ?? fallback.disk_pct,
    net_bps: hasNet ? (latest.net_rx_bps ?? 0) + (latest.net_tx_bps ?? 0) : fallback.net_bps,
    net_rx_bps: hasNet ? (latest.net_rx_bps ?? 0) : fallback.net_rx_bps,
    net_tx_bps: hasNet ? (latest.net_tx_bps ?? 0) : fallback.net_tx_bps,
    load1: latest.load1 ?? fallback.load1,
  };
}

const HALF: Record<MetricsRange, string> = {
  "1h": "30 min",
  "12h": "6 h",
  "24h": "12 h",
  "7d": "3.5 days",
  "30d": "15 days",
};

// deltaLabel names what a KPI delta compares against: the older half of
// the fetched range ("vs previous 30 min").
export function deltaLabel(range: MetricsRange): string {
  return `vs previous ${HALF[range]}`;
}

// withNetTotal adds net_bps (and its peak), received plus sent, so the
// network KPI charts one figure. A point reporting neither stays without it.
export function withNetTotal(points: MetricPoint[]): MetricPoint[] {
  return points.map((p) => {
    if (typeof p.net_rx_bps !== "number" && typeof p.net_tx_bps !== "number") return p;
    const rx = p.net_rx_bps ?? 0;
    const tx = p.net_tx_bps ?? 0;
    return {
      ...p,
      net_bps: rx + tx,
      net_bps_max: (p.net_rx_bps_max ?? rx) + (p.net_tx_bps_max ?? tx),
    };
  });
}

// percentCeiling is a percentage sparkline's top: the round step above its
// peak, at least floor and at most 100, so an idle 3 % CPU reads as a low
// line instead of noise stretched to full height (or a flat floor at 0-100).
export function percentCeiling(rows: ChartRow[], field: string, floor = 10): number {
  const values = rows.map((r) => r[field]).filter((v): v is number => typeof v === "number" && Number.isFinite(v));
  const top = niceTicks(values).at(-1) ?? 100;
  return Math.min(100, Math.max(floor, top));
}

// formatAge renders an elapsed time compactly: "just now" under a minute,
// then "5m", "3h", "2d".
export function formatAge(ms: number): string {
  const s = Math.max(0, Math.floor(ms / 1000));
  if (s < 60) return "just now";
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m`;
  const h = Math.floor(m / 60);
  if (h < 48) return `${h}h`;
  return `${Math.floor(h / 24)}d`;
}

export type Presence = {
  // tone picks the dot: live (emerald), offline (muted), waiting (faint).
  tone: "live" | "offline" | "waiting";
  // label is the short text beside the dot ("just now", "offline 5m").
  label: string;
  // title is the long form for a hover title ("Last seen <date>").
  title: string;
};

// serverPresence is a server card's status: "just now" while the agent is
// connected, "offline 5m" once it dropped, "waiting" before it ever came.
export function serverPresence(online: boolean, lastSeenAt: string | null, now: number): Presence {
  const seen = lastSeenAt ? new Date(lastSeenAt).getTime() : NaN;
  if (online) {
    return { tone: "live", label: "just now", title: "Online: the agent is connected" };
  }
  if (Number.isNaN(seen)) {
    return { tone: "waiting", label: "waiting", title: "Waiting for the agent to connect" };
  }
  const age = formatAge(now - seen);
  return {
    tone: "offline",
    label: age === "just now" ? "offline" : `offline ${age}`,
    title: `Offline since ${new Date(seen).toLocaleString()}`,
  };
}

// formatClock renders a freshness time: "05:06 PM".
export function formatClock(ms: number): string {
  return new Date(ms).toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });
}

// appLabel is an app's name for the UI. An app not reconciled yet has none
// and is never shown by its raw id.
export function appLabel(name: string | undefined | null): string {
  return name?.trim() || "Unnamed app";
}
