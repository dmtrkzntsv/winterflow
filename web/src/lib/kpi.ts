// Pure helpers behind the KPI rows, the dashboard's server cards and the
// freshness line: the newest value of a series, its delta within the
// fetched range, relative ages and presence labels. No DOM, so they test
// in isolation.

import { deltaPercent, niceTicks, splitHalves } from "@/lib/chart-scale";
import type { ChartRow, MetricPoint, MetricsRange } from "@/lib/metrics";

// latestValue is the newest numeric value of field, skipping the null gap
// rows toChartRows inserts.
export function latestValue(rows: ChartRow[], field: string): number | undefined {
  for (let i = rows.length - 1; i >= 0; i--) {
    const v = rows[i][field];
    if (typeof v === "number" && Number.isFinite(v)) return v;
  }
  return undefined;
}

// periodDelta is the change of the newer half of the range against the
// older half, in percent (deltaPercent), or null without enough data.
export function periodDelta(rows: ChartRow[], field: string): number | null {
  const { previous, current } = splitHalves(rows.map((r) => r[field]));
  return deltaPercent(current, previous);
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

// appLabel is an app's display name; an app not reconciled yet has none and
// is named by its id prefix.
export function appLabel(name: string | undefined, id: string): string {
  return name?.trim() || id.slice(0, 8);
}
