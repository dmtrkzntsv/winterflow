import type { LucideIcon } from "lucide-react";
import { ChartSpline } from "lucide-react";

import { cn } from "@/lib/utils";
import { formatHeading, tooltipRows, type TooltipItem } from "@/lib/chart-scale";

type SeriesRef = { key: string; label: string; color: string };

// MetricTooltip is the chart kit's hover card (recharts passes active and
// payload): a time heading, then one row per series - colour dot, muted
// label left, value right in tabular figures - and a Total row for a stack.
// It composes ui/chart's container rather than replacing its tooltip.
export function MetricTooltip({
  active,
  payload,
  series,
  format,
  total = false,
}: {
  active?: boolean;
  payload?: (TooltipItem & { payload?: { t?: unknown } })[];
  series: SeriesRef[];
  format: (v: number) => string;
  total?: boolean;
}) {
  if (!active || !payload?.length) return null;
  const { rows, total: sum } = tooltipRows(payload, series, format, total);
  if (rows.length === 0) return null;
  const t = payload[0]?.payload?.t;
  return (
    <div
      data-testid="chart-tooltip"
      className="grid min-w-[9rem] items-start gap-1.5 rounded-lg border border-border/70 bg-popover/95 px-2.5 py-2 text-xs text-popover-foreground shadow-lg backdrop-blur-sm"
    >
      {typeof t === "number" ? <div className="font-medium tabular-nums">{formatHeading(t)}</div> : null}
      <div className="grid gap-1.5">
        {rows.map((r) => (
          <div key={r.key} className="flex items-center gap-2 leading-none">
            <span className="size-2 shrink-0 rounded-full" style={{ backgroundColor: r.color }} />
            <span className="flex-1 truncate text-muted-foreground">{r.label}</span>
            <span className="pl-3 font-medium text-foreground tabular-nums">{r.value}</span>
          </div>
        ))}
      </div>
      {sum !== undefined ? (
        <div className="flex items-center justify-between border-t border-border/70 pt-1.5 leading-none">
          <span className="text-muted-foreground">Total</span>
          <span className="font-medium text-foreground tabular-nums">{sum}</span>
        </div>
      ) : null}
    </div>
  );
}

// MetricLegend is the key under a chart of several series (or a stack):
// round 8px dots and muted 12px labels in series order, centred, wrapping
// between items and never inside a label.
export function MetricLegend({ series }: { series: SeriesRef[] }) {
  return (
    <div
      data-testid="chart-legend"
      className="flex flex-wrap items-center justify-center gap-x-3 gap-y-1 pt-2 text-xs text-muted-foreground [&>div]:whitespace-nowrap"
    >
      {series.map((s) => (
        <div key={s.key} className="flex items-center gap-1.5">
          <span className="size-2 shrink-0 rounded-full" style={{ backgroundColor: s.color }} />
          {s.label}
        </div>
      ))}
    </div>
  );
}

// ChartEmpty fills a chart's body when there is nothing to draw: a centred
// lucide icon and one muted line.
export function ChartEmpty({
  message,
  icon: Icon = ChartSpline,
  className,
}: {
  message: string;
  icon?: LucideIcon;
  className?: string;
}) {
  return (
    <div
      data-testid="chart-empty"
      className={cn("flex h-full flex-col items-center justify-center gap-2 px-4 text-center text-muted-foreground", className)}
    >
      <Icon className="size-5 opacity-70" aria-hidden />
      <p className="text-sm font-medium">{message}</p>
    </div>
  );
}
