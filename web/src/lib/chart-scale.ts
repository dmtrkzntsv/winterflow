// Pure helpers behind the chart kit: round value-axis ticks per unit, the
// axis width those ticks need, round time ticks and the delta pill's
// figure. Kept out of the components so they test without a DOM.

import { formatPercent, formatRate, formatSize } from "@/lib/metrics";

// AxisScale is what a value axis measures, which decides its round steps
// and how a tick reads:
//   percent  0/25/50/75/100 (or tighter under 50 %)
//   bytes    1/2/5 x 10^n in one binary unit ("0 / 200 / 400 KB")
//   rate     bytes per second, as bytes with "/s"
//   integer  whole steps only (load averages, counts)
//   number   1/2/2.5/5 x 10^n, labelled by the chart's own formatter
export type AxisScale = "percent" | "bytes" | "rate" | "integer" | "number";

export type NiceAxis = {
  ticks: number[];
  domain: [number, number];
  // label renders one tick ("400 KB", "25%", "4").
  label: (v: number) => string;
};

const STEPS = [1, 2, 2.5, 5, 10];
const ROUND_STEPS = [1, 2, 5, 10];

// trim drops float noise and trailing zeros: 0.30000000000000004 -> "0.3".
function trim(v: number): string {
  return String(Number(v.toPrecision(12)));
}

// niceTicks returns about count+1 round ticks from zero (or the lowest
// negative value) up past the largest value: steps of m x 10^n for m in
// steps. A flat or empty series still gets an axis, [0, 1].
export function niceTicks(values: number[], count = 4, steps: number[] = STEPS): number[] {
  const finite = values.filter(Number.isFinite);
  const lo = Math.min(0, ...finite);
  const hi = Math.max(0, ...finite);
  if (lo === hi) return [0, 1];
  const rough = (hi - lo) / count;
  const magnitude = 10 ** Math.floor(Math.log10(rough));
  const step = steps.map((m) => m * magnitude).find((s) => s >= rough * (1 - 1e-9)) ?? 10 * magnitude;
  const first = Math.floor(lo / step + 1e-9);
  const last = Math.ceil(hi / step - 1e-9);
  return Array.from({ length: last - first + 1 }, (_, i) => Number(((first + i) * step).toPrecision(12)));
}

const UNITS = ["B", "KB", "MB", "GB", "TB", "PB"];

// byteUnit is the binary unit (as a power of 1024) the largest value reads
// in: 400_000 -> 1 (KB).
function byteUnit(hi: number): number {
  let i = 0;
  while (hi >= 1024 && i < UNITS.length - 1) {
    hi /= 1024;
    i++;
  }
  return i;
}

// niceAxis picks the ticks, domain and tick label of a value axis over
// values (pass stack totals for a stacked chart). fallback labels the
// "number" scale.
export function niceAxis(
  values: (number | null | undefined)[],
  scale: AxisScale,
  fallback: (v: number) => string = trim,
): NiceAxis {
  const finite = values.filter((v): v is number => typeof v === "number" && Number.isFinite(v));
  const hi = Math.max(0, ...finite);
  let ticks: number[];
  let label: (v: number) => string;
  switch (scale) {
    case "percent":
      // A flat 0 % series (an idle app) still reads as a percent axis.
      if (hi === 0) ticks = [0, 0.5, 1];
      else if (hi > 50 && hi <= 100) ticks = [0, 25, 50, 75, 100];
      else ticks = niceTicks(finite);
      label = (v) => `${trim(v)}%`;
      break;
    case "bytes":
    case "rate": {
      const suffix = scale === "rate" ? "/s" : "";
      let unit = byteUnit(hi);
      let div = 1024 ** unit;
      ticks = hi === 0 ? [0, 1] : niceTicks(finite.map((v) => v / div), 4, ROUND_STEPS);
      // A top tick of 1000+ ("1000 KB") reads better one unit up ("1 MB").
      if (ticks[ticks.length - 1] >= 1000 && unit < UNITS.length - 1) {
        unit++;
        div *= 1024;
        ticks = niceTicks(finite.map((v) => v / div), 4, ROUND_STEPS);
      }
      ticks = ticks.map((t) => Number((t * div).toPrecision(12)));
      label = (v) => `${trim(v / div)} ${UNITS[unit]}${suffix}`;
      break;
    }
    case "integer":
      ticks = niceTicks(finite, 4, ROUND_STEPS);
      if (ticks.length > 1 && ticks[1] - ticks[0] < 1) ticks = Array.from({ length: Math.ceil(hi) + 1 }, (_, i) => i);
      if (ticks.length < 2) ticks = [0, 1];
      label = (v) => String(Math.round(v));
      break;
    default:
      ticks = niceTicks(finite);
      label = fallback;
  }
  return { ticks, domain: [ticks[0], ticks[ticks.length - 1]], label };
}

const MINUTE = 60_000;
const HOUR = 60 * MINUTE;
const DAY = 24 * HOUR;
// Round time steps, finest first: minutes that divide an hour, hours that
// divide a day, then days and weeks.
const TIME_STEPS = [
  MINUTE, 2 * MINUTE, 5 * MINUTE, 10 * MINUTE, 15 * MINUTE, 30 * MINUTE,
  HOUR, 2 * HOUR, 3 * HOUR, 6 * HOUR, 12 * HOUR,
  DAY, 2 * DAY, 7 * DAY, 14 * DAY,
];

// timeTicks returns time-axis ticks on round local boundaries (every 10
// minutes, 3 hours, midnight...) inside [start, end] ms: the finest step
// that keeps at most maxTicks, evenly spaced. The newest point is not forced
// in, so the last gap never crowds. tzOffsetMinutes is
// Date#getTimezoneOffset (local by default).
export function timeTicks(
  start: number,
  end: number,
  maxTicks = 8,
  tzOffsetMinutes = new Date(Number.isFinite(start) ? start : 0).getTimezoneOffset(),
): number[] {
  if (!Number.isFinite(start) || !Number.isFinite(end) || end <= start) return [];
  const span = end - start;
  const step = TIME_STEPS.find((s) => span / s < maxTicks) ?? TIME_STEPS[TIME_STEPS.length - 1];
  const shift = -tzOffsetMinutes * MINUTE; // local = utc + shift
  const first = Math.ceil((start + shift) / step) * step - shift;
  const ticks: number[] = [];
  for (let t = first; t <= end; t += step) ticks.push(t);
  return ticks;
}

// sparkDomain is a sparkline's value axis: zero to max when given, else the
// data's own span. A flat series is padded so it draws as a solid line at
// mid-height rather than on a degenerate axis.
export function sparkDomain(values: (number | null | undefined)[], max?: number): [number, number] {
  if (max !== undefined) return [0, max];
  const finite = values.filter((v): v is number => typeof v === "number" && Number.isFinite(v));
  if (finite.length === 0) return [0, 1];
  const lo = Math.min(...finite);
  const hi = Math.max(...finite);
  if (lo !== hi) return [lo, hi];
  const pad = Math.abs(lo) / 2 || 1;
  return [lo - pad, hi + pad];
}

// scaleFor infers the axis scale from a chart's value formatter, so callers
// that pass formatPercent/formatSize/formatRate need not say it twice.
export function scaleFor(format: (v: number) => string): AxisScale {
  if (format === formatPercent) return "percent";
  if (format === formatSize) return "bytes";
  if (format === formatRate) return "rate";
  return "number";
}

// axisWidth is the y-axis width (px) its widest tick label needs at the
// kit's 11px tick font, plus the tick margin. recharts 2 has no "auto".
export function axisWidth(labels: string[]): number {
  const longest = Math.max(1, ...labels.map((l) => l.length));
  // ~7px per 11px glyph, plus recharts' tick size (6) and margin (8).
  return Math.ceil(longest * 7) + 18;
}

// stackTotals is each row's sum over keys: the heights a stacked chart's
// axis must reach.
export function stackTotals(rows: Record<string, unknown>[], keys: string[]): number[] {
  return rows.map((row) => keys.reduce((sum, k) => sum + (typeof row[k] === "number" ? (row[k] as number) : 0), 0));
}

// valuesOf is every numeric value under keys, for an unstacked axis.
export function valuesOf(rows: Record<string, unknown>[], keys: string[]): number[] {
  return rows.flatMap((row) => keys.map((k) => row[k]).filter((v): v is number => typeof v === "number"));
}

// average is the mean of the finite values, or null without any.
export function average(values: (number | null | undefined)[]): number | null {
  const finite = values.filter((v): v is number => typeof v === "number" && Number.isFinite(v));
  if (finite.length === 0) return null;
  return finite.reduce((a, b) => a + b, 0) / finite.length;
}

// formatDelta renders a delta pill's figure: "+12.1%", "-4%", "+130%", "0%".
export function formatDelta(delta: number): string {
  const abs = Math.abs(delta);
  const n = abs < 100 ? Number(abs.toFixed(1)) : Math.round(abs);
  if (n === 0) return "0%";
  return `${delta > 0 ? "+" : "-"}${n}%`;
}

// formatHeading renders a tooltip heading: "Oct 11, 05:06 PM".
export function formatHeading(ms: number): string {
  return new Date(ms).toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

export type TooltipItem = { dataKey?: unknown; value?: unknown; color?: string };
export type TooltipRow = { key: string; label: string; color: string; value: string };
type SeriesRef = { key: string; label: string; color: string };

// tooltipRows lays out a hovered point: one row per series in series order
// (never recharts' payload order) with its formatted value, skipping series
// without a value there, plus the formatted sum when total is set and two
// or more rows show.
export function tooltipRows(
  payload: TooltipItem[],
  series: SeriesRef[],
  format: (v: number) => string,
  total = false,
): { rows: TooltipRow[]; total?: string } {
  const byKey = new Map(payload.map((p) => [String(p.dataKey), p]));
  const rows: TooltipRow[] = [];
  let sum = 0;
  for (const s of series) {
    const v = byKey.get(s.key)?.value;
    if (typeof v !== "number" || !Number.isFinite(v)) continue;
    sum += v;
    rows.push({ key: s.key, label: s.label, color: s.color, value: format(v) });
  }
  return total && rows.length > 1 ? { rows, total: format(sum) } : { rows };
}
