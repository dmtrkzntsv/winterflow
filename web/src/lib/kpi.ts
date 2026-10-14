// Pure helpers behind the KPI rows, the dashboard's server cards and the
// freshness line: the newest value of a series, the live headline figures,
// relative ages and presence labels. No DOM, so they test
// in isolation.

import { niceTicks } from "@/lib/chart-scale";
import { fullestDisk, percentOf, type ChartRow, type MetricPoint, type ServerSample } from "@/lib/metrics";

// latestValue is the newest numeric value of field, skipping the null gap
// rows toChartRows inserts.
export function latestValue(rows: ChartRow[], field: string): number | undefined {
  for (let i = rows.length - 1; i >= 0; i--) {
    const v = rows[i][field];
    if (typeof v === "number" && Number.isFinite(v)) return v;
  }
  return undefined;
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

// lastSeenText is a server's "last seen 5m ago" (or "last seen just now"),
// from the timestamp itself. Empty for a server that never connected.
export function lastSeenText(lastSeenAt: string | null, now: number): string {
  const seen = lastSeenAt ? new Date(lastSeenAt).getTime() : NaN;
  if (Number.isNaN(seen)) return "";
  const age = formatAge(now - seen);
  return age === "just now" ? "last seen just now" : `last seen ${age} ago`;
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
