import { Area, AreaChart, CartesianGrid, XAxis, YAxis } from "recharts";

import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import {
  ChartContainer,
  ChartLegend,
  ChartLegendContent,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from "@/components/ui/chart";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import {
  METRICS_RANGES,
  formatTick,
  type ChartRow,
  type MetricsMode,
  type MetricsRange,
} from "@/lib/metrics";

export type ChartSeries = { key: string; label: string; color: string };

type MetricChartCardProps = {
  title: string;
  description?: string;
  rows: ChartRow[];
  series: ChartSeries[];
  range: MetricsRange;
  format: (v: number) => string;
  // stacked draws the series on top of each other (per-app CPU/memory).
  stacked?: boolean;
  // empty replaces the chart with this message (no data yet, unavailable).
  empty?: string;
};

// MetricChartCard is one titled area chart. rows come from toChartRows or
// stackApps; a null value breaks the line (a gap where nothing reported).
export function MetricChartCard({
  title,
  description,
  rows,
  series,
  range,
  format,
  stacked,
  empty,
}: MetricChartCardProps) {
  const config: ChartConfig = Object.fromEntries(
    series.map((s) => [s.key, { label: s.label, color: s.color }]),
  );
  // Rows whose series are all null (first minutes, rates not reported yet)
  // are no data either: never draw an empty axis.
  const hasValues = rows.some((r) => series.some((s) => typeof r[s.key] === "number"));
  const message = empty ?? (hasValues ? undefined : "No data yet");
  return (
    <Card data-testid={`chart-${title}`}>
      <CardHeader className="pb-2">
        <CardTitle className="text-sm font-medium">{title}</CardTitle>
        {description ? <CardDescription>{description}</CardDescription> : null}
      </CardHeader>
      <CardContent>
        {message ? (
          <div className="flex h-[180px] items-center justify-center text-sm text-muted-foreground">
            {message}
          </div>
        ) : (
          <ChartContainer config={config} className="aspect-auto h-[180px] w-full">
            <AreaChart data={rows} margin={{ left: 0, right: 8, top: 4 }}>
              <CartesianGrid vertical={false} />
              <XAxis
                dataKey="t"
                type="number"
                scale="time"
                domain={["dataMin", "dataMax"]}
                tickFormatter={(v: number) => formatTick(v, range)}
                tickLine={false}
                axisLine={false}
                minTickGap={32}
              />
              <YAxis tickFormatter={(v: number) => format(v)} tickLine={false} axisLine={false} width={64} />
              <ChartTooltip
                content={
                  <ChartTooltipContent
                    labelFormatter={(_, payload) => {
                      const t = payload?.[0]?.payload?.t;
                      return typeof t === "number" ? new Date(t).toLocaleString() : "";
                    }}
                    formatter={(value, name) => (
                      <span>
                        {config[String(name)]?.label ?? name}: {format(Number(value))}
                      </span>
                    )}
                  />
                }
              />
              {series.length > 1 ? <ChartLegend content={<ChartLegendContent />} /> : null}
              {series.map((s) => (
                <Area
                  key={s.key}
                  dataKey={s.key}
                  type="monotone"
                  stroke={`var(--color-${s.key})`}
                  fill={`var(--color-${s.key})`}
                  fillOpacity={stacked ? 0.4 : 0.15}
                  stackId={stacked ? "stack" : undefined}
                  connectNulls={false}
                  isAnimationActive={false}
                  dot={false}
                />
              ))}
            </AreaChart>
          </ChartContainer>
        )}
      </CardContent>
    </Card>
  );
}

// RangeSelect picks the chart range.
export function RangeSelect({
  value,
  onChange,
}: {
  value: MetricsRange;
  onChange: (r: MetricsRange) => void;
}) {
  return (
    <ToggleGroup
      type="single"
      variant="outline"
      size="sm"
      value={value}
      onValueChange={(v) => v && onChange(v as MetricsRange)}
      aria-label="Range"
    >
      {METRICS_RANGES.map((r) => (
        <ToggleGroupItem key={r.value} value={r.value} aria-label={r.label}>
          {r.value}
        </ToggleGroupItem>
      ))}
    </ToggleGroup>
  );
}

// ModeToggle switches between averages and peaks.
export function ModeToggle({
  value,
  onChange,
}: {
  value: MetricsMode;
  onChange: (m: MetricsMode) => void;
}) {
  return (
    <ToggleGroup
      type="single"
      variant="outline"
      size="sm"
      value={value}
      onValueChange={(v) => v && onChange(v as MetricsMode)}
      aria-label="Average or peak"
    >
      <ToggleGroupItem value="avg">Average</ToggleGroupItem>
      <ToggleGroupItem value="peak">Peak</ToggleGroupItem>
    </ToggleGroup>
  );
}
