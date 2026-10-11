import { Area, AreaChart, YAxis } from "recharts";

import { ChartContainer } from "@/components/ui/chart";
import type { ChartRow } from "@/lib/metrics";

// Sparkline is a tiny axis-less area chart of one field (rows from
// toChartRows). Nothing renders without at least two points.
export function Sparkline({
  rows,
  field,
  color,
  max,
}: {
  rows: ChartRow[];
  field: string;
  color: string;
  max?: number;
}) {
  if (rows.filter((r) => typeof r[field] === "number").length < 2) return null;
  return (
    <ChartContainer
      config={{ [field]: { color } }}
      className="aspect-auto h-8 w-full"
      data-testid={`sparkline-${field}`}
    >
      <AreaChart data={rows} margin={{ top: 2, bottom: 0, left: 0, right: 0 }}>
        <YAxis hide domain={[0, max ?? "auto"]} />
        <Area
          dataKey={field}
          type="monotone"
          stroke={`var(--color-${field})`}
          fill={`var(--color-${field})`}
          fillOpacity={0.15}
          isAnimationActive={false}
          connectNulls={false}
          dot={false}
        />
      </AreaChart>
    </ChartContainer>
  );
}
