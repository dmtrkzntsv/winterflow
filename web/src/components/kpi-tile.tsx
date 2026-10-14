import type { ReactNode } from "react";

import { Card } from "@/components/ui/card";
import { Sparkline } from "@/components/sparkline";
import { cn } from "@/lib/utils";
import type { ChartRow } from "@/lib/metrics";

export type KpiSpark = { rows: ChartRow[]; field: string; color: string; max?: number };

type KpiTileProps = {
  label: string;
  // value is the formatted headline figure ("42%", "1.2 GB").
  value: string;
  spark?: KpiSpark;
  // sub is a muted line under the figure ("of 1 GB limit").
  sub?: ReactNode;
  className?: string;
  testId?: string;
};

// KpiTile is one headline number: a muted label, a 30px figure in
// proportional digits (never the series colour), then a sub-line and an
// optional sparkline with a dot on the newest point.
export function KpiTile({
  label,
  value,
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
// and a line.
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
      <div className="frost h-3.5 w-20 rounded" />
    </Card>
  );
}
