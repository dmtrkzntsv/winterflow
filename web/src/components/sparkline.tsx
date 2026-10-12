import { useId } from "react";
import { Area, AreaChart, YAxis } from "recharts";

import { ChartContainer } from "@/components/ui/chart";
import { cn } from "@/lib/utils";
import type { ChartRow } from "@/lib/metrics";

// Sparkline is a tiny axis-less area chart of one field (rows from
// toChartRows): a 2px line over a gradient wash, with a dot on the newest
// point. Without max the axis spans the data (the shape of the trend);
// with max it runs from zero to max (a percentage). Nothing renders without
// at least two points.
export function Sparkline({
  rows,
  field,
  color,
  max,
  className,
}: {
  rows: ChartRow[];
  field: string;
  color: string;
  max?: number;
  className?: string;
}) {
  const fade = "spark" + useId().replace(/[^a-zA-Z0-9_-]/g, "");
  let last = -1;
  let count = 0;
  rows.forEach((r, i) => {
    if (typeof r[field] === "number") {
      last = i;
      count++;
    }
  });
  if (count < 2) return null;
  const stroke = `var(--color-${field})`;
  return (
    <ChartContainer
      config={{ [field]: { color } }}
      className={cn("aspect-auto h-8 w-full", className)}
      data-testid={`sparkline-${field}`}
    >
      <AreaChart data={rows} margin={{ top: 4, right: 4, bottom: 2, left: 4 }}>
        <defs>
          <linearGradient id={fade} x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor={stroke} stopOpacity={0.28} />
            <stop offset="100%" stopColor={stroke} stopOpacity={0.02} />
          </linearGradient>
        </defs>
        <YAxis hide domain={max === undefined ? ["dataMin", "dataMax"] : [0, max]} />
        <Area
          dataKey={field}
          type="monotone"
          baseValue={max === undefined ? "dataMin" : undefined}
          stroke={stroke}
          strokeWidth={2}
          fill={`url(#${fade})`}
          isAnimationActive={false}
          connectNulls={false}
          dot={(p: { index: number; cx?: number; cy?: number }) =>
            p.index === last && p.cx !== undefined && p.cy !== undefined ? (
              <circle
                key="last"
                data-last-dot={field}
                cx={p.cx}
                cy={p.cy}
                r={3}
                fill={stroke}
                stroke="var(--card)"
                strokeWidth={2}
              />
            ) : (
              <g key={p.index} />
            )
          }
        />
      </AreaChart>
    </ChartContainer>
  );
}
