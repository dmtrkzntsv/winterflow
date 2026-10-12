import type { ReactNode } from "react";
import { ArrowDownRight, ArrowUpRight } from "lucide-react";

import { Card } from "@/components/ui/card";
import { Sparkline } from "@/components/sparkline";
import { cn } from "@/lib/utils";
import { formatDelta } from "@/lib/chart-scale";
import { deltaDirection } from "@/lib/kpi";
import type { ChartRow } from "@/lib/metrics";

export type KpiSpark = { rows: ChartRow[]; field: string; color: string; max?: number };

type KpiTileProps = {
  label: string;
  // value is the formatted headline figure ("42%", "1.2 GB").
  value: string;
  // delta is the percent change vs the previous period (periodDelta);
  // null or undefined shows no pill.
  delta?: number | null;
  deltaLabel?: string;
  // significant=false keeps a real but trivial move neutral (isSignificant);
  // a delta that shows as 0 % is always neutral, without an arrow.
  significant?: boolean;
  // goodWhen says which direction is good news: "up" (default) paints a
  // rise green, "down" paints a rise red (resource usage), "neutral" keeps
  // both muted.
  goodWhen?: "up" | "down" | "neutral";
  spark?: KpiSpark;
  // sub is a muted line under the figure ("of 1 GB limit").
  sub?: ReactNode;
  className?: string;
  testId?: string;
};

function DeltaPill({
  delta,
  label,
  goodWhen,
  significant,
}: {
  delta: number;
  label: string;
  goodWhen: "up" | "down" | "neutral";
  significant: boolean;
}) {
  const direction = deltaDirection(delta);
  const good =
    goodWhen === "neutral" || direction === "flat" || !significant
      ? undefined
      : (direction === "up") === (goodWhen === "up");
  const Arrow = direction === "up" ? ArrowUpRight : direction === "down" ? ArrowDownRight : null;
  return (
    // On a narrow tile the label drops under the pill as one line instead
    // of wrapping word by word beside it.
    <div className="flex flex-wrap items-center gap-x-1.5 gap-y-1 text-xs" data-testid="kpi-delta">
      <span
        data-tone={good === undefined ? "neutral" : good ? "good" : "bad"}
        className={cn(
          "inline-flex items-center gap-0.5 rounded-full px-1.5 py-0.5 font-medium tabular-nums",
          good === undefined && "bg-muted text-muted-foreground",
          good === true && "bg-emerald-500/12 text-emerald-700 dark:text-emerald-400",
          good === false && "bg-red-500/12 text-red-700 dark:text-red-400",
        )}
      >
        {Arrow ? <Arrow className="size-3" aria-hidden /> : null}
        {direction === "flat" ? "0%" : formatDelta(delta)}
      </span>
      <span className="truncate whitespace-nowrap text-muted-foreground">{label}</span>
    </div>
  );
}

// KpiTile is one headline number: a muted label, a 30px figure in
// proportional digits (never the series colour), then an optional delta
// pill vs the previous period, a sub-line and a sparkline with a dot on the
// newest point.
export function KpiTile({
  label,
  value,
  delta,
  deltaLabel = "vs previous",
  goodWhen = "up",
  significant = true,
  spark,
  sub,
  className,
  testId,
}: KpiTileProps) {
  return (
    <Card
      data-slot="widget-card"
      data-testid={testId ?? `kpi-${label}`}
      className={cn("relative flex min-w-0 flex-col gap-1.5 p-3.5", className)}
    >
      <div className="flex min-h-5 items-center gap-1.5">
        <h3 className="truncate text-sm font-medium text-muted-foreground">{label}</h3>
      </div>
      <div className="pt-1 text-3xl leading-none font-semibold tracking-tight">{value}</div>
      {typeof delta === "number" && Number.isFinite(delta) ? (
        <DeltaPill delta={delta} label={deltaLabel} goodWhen={goodWhen} significant={significant} />
      ) : null}
      {sub ? <div className="line-clamp-2 text-xs text-muted-foreground">{sub}</div> : null}
      {spark ? (
        <div className="-mx-1 mt-auto min-h-8 flex-1">
          <Sparkline rows={spark.rows} field={spark.field} color={spark.color} max={spark.max} className="h-10" />
        </div>
      ) : null}
    </Card>
  );
}

// KpiTileSkeleton is a KPI tile while it loads: the label, a figure block
// and a pill.
export function KpiTileSkeleton({ label, className }: { label?: string; className?: string }) {
  return (
    <Card
      data-slot="widget-card"
      role="status"
      aria-label={label ? `Loading ${label}` : "Loading"}
      className={cn("flex min-w-0 flex-col gap-1.5 p-3.5", className)}
    >
      {label ? (
        <h3 className="truncate text-sm font-medium text-muted-foreground">{label}</h3>
      ) : (
        <div className="frost h-3.5 w-16 rounded" />
      )}
      <div className="frost mt-1 h-8 w-28 rounded-md" />
      <div className="frost h-5 w-14 rounded-full" />
    </Card>
  );
}
