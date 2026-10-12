import { useId, type ReactNode } from "react";
import { Info } from "lucide-react";
import { Area, AreaChart, CartesianGrid, XAxis, YAxis } from "recharts";

import { Card } from "@/components/ui/card";
import { ChartContainer, ChartTooltip, type ChartConfig } from "@/components/ui/chart";
import { ChartEmpty, MetricLegend, MetricTooltip } from "@/components/chart-parts";
import { cn } from "@/lib/utils";
import { axisWidth, niceAxis, scaleFor, stackTotals, valuesOf, type AxisScale } from "@/lib/chart-scale";
import { formatTick, type ChartRow, type MetricsRange } from "@/lib/metrics";

export type ChartSeries = { key: string; label: string; color: string };

// Every cartesian axis: no tick marks or rule, 11px muted labels clear of
// the plot (the muted fill comes from ChartContainer).
const AXIS = { tickLine: false, axisLine: false, tickMargin: 8, fontSize: 11 } as const;

type MetricChartCardProps = {
  title: string;
  // description is a hint behind an info icon beside the title (the card
  // has no subtitle line).
  description?: string;
  rows: ChartRow[];
  series: ChartSeries[];
  range: MetricsRange;
  format: (v: number) => string;
  // scale picks the y-axis' round steps; inferred from format by default
  // (formatPercent, formatSize, formatRate), else "number".
  scale?: AxisScale;
  // stacked draws the series on top of each other (per-app CPU/memory).
  stacked?: boolean;
  // lines draws unfilled, unstacked lines: for many overlapping series
  // (temperature sensors, load averages) whose fills would blend together.
  lines?: boolean;
  // empty replaces the chart with this message (no data yet, unavailable).
  empty?: string;
  // height is the plot's height in px (default 200), or "fill" to take
  // whatever height the card is given (a grid row, an h-* class).
  height?: number | "fill";
  // actions sit at the card's top right (refresh, menus).
  actions?: ReactNode;
  className?: string;
};

// lastIndex is the newest row index where key has a value.
function lastIndex(rows: ChartRow[], key: string): number {
  for (let i = rows.length - 1; i >= 0; i--) {
    if (typeof rows[i][key] === "number") return i;
  }
  return -1;
}

// ChartCardFrame is the chart card's shell: the widget-card slot (14px
// padding, 6px gap), a muted title row and the body.
function ChartCardFrame({
  title,
  description,
  actions,
  className,
  testId,
  children,
}: {
  title: string;
  description?: string;
  actions?: ReactNode;
  className?: string;
  testId?: string;
  children: ReactNode;
}) {
  return (
    // min-w-0 lets the card shrink inside a grid cell instead of widening
    // the page to fit the chart.
    <Card
      data-slot="widget-card"
      data-testid={testId}
      className={cn("relative flex min-w-0 flex-col gap-1.5 p-3.5", className)}
    >
      <div className="flex min-h-5 items-center gap-1.5 pr-1">
        <h3 className="truncate text-sm font-medium text-muted-foreground">{title}</h3>
        {description ? (
          <span title={description} aria-label={description} className="text-muted-foreground/70">
            <Info className="size-3.5" aria-hidden />
          </span>
        ) : null}
        {actions ? <div className="ml-auto flex items-center gap-1">{actions}</div> : null}
      </div>
      {children}
    </Card>
  );
}

// MetricChartCard is one titled time-series card. rows come from
// toChartRows or stackApps; a null value breaks the line (a gap where
// nothing reported).
export function MetricChartCard({
  title,
  description,
  rows,
  series,
  range,
  format,
  scale,
  stacked,
  lines,
  empty,
  height = 200,
  actions,
  className,
}: MetricChartCardProps) {
  const stack = Boolean(stacked && !lines);
  const fade = "fade" + useId().replace(/[^a-zA-Z0-9_-]/g, "");
  const config: ChartConfig = Object.fromEntries(series.map((s) => [s.key, { label: s.label, color: s.color }]));
  const keys = series.map((s) => s.key);
  // Rows whose series are all null (first minutes, rates not reported yet)
  // are no data either: never draw an empty axis.
  const hasValues = rows.some((r) => keys.some((k) => typeof r[k] === "number"));
  const message = empty ?? (hasValues ? undefined : "No data yet");
  const axis = niceAxis(stack ? stackTotals(rows, keys) : valuesOf(rows, keys), scale ?? scaleFor(format), format);
  const yWidth = axisWidth(axis.ticks.map(axis.label));
  // A single or stacked fill gets the full wash; overlapping fills a
  // lighter one so the ones behind still show.
  const wash = stack || series.length === 1 ? 0.32 : 0.16;
  const fill = height === "fill";

  return (
    <ChartCardFrame
      title={title}
      description={description}
      actions={actions}
      className={cn(fill && "h-full", className)}
      testId={`chart-${title}`}
    >
      <div className={cn("min-h-0", fill && "flex-1")} style={fill ? undefined : { height }}>
        {message ? (
          <ChartEmpty message={message} />
        ) : (
          <ChartContainer config={config} className="aspect-auto h-full w-full tabular-nums">
            <AreaChart data={rows} margin={{ top: 8, right: 8, bottom: 0, left: 0 }}>
              <defs>
                {/* Each colour fades toward the axis. */}
                {series.map((s) => (
                  <linearGradient key={s.key} id={`${fade}-${s.key}`} x1="0" y1="0" x2="0" y2="1">
                    <stop offset="0%" stopColor={`var(--color-${s.key})`} stopOpacity={wash} />
                    <stop offset="100%" stopColor={`var(--color-${s.key})`} stopOpacity={0.02} />
                  </linearGradient>
                ))}
              </defs>
              <CartesianGrid vertical={false} />
              <XAxis
                dataKey="t"
                type="number"
                scale="time"
                domain={["dataMin", "dataMax"]}
                tickFormatter={(v: number) => formatTick(v, range)}
                interval="preserveStartEnd"
                minTickGap={24}
                {...AXIS}
              />
              <YAxis
                ticks={axis.ticks}
                domain={axis.domain}
                width={yWidth}
                {...AXIS}
                // A plain <text>: recharts' own tick wraps a label that
                // nearly fills the measured width ("60 / MB/s").
                tick={({ x, y, payload }: { x: number; y: number; payload: { value: number } }) => (
                  <text
                    x={x}
                    y={y}
                    dy={4}
                    textAnchor="end"
                    fontSize={11}
                    className="recharts-cartesian-axis-tick-value fill-muted-foreground"
                  >
                    {axis.label(payload.value)}
                  </text>
                )}
              />
              <ChartTooltip
                cursor={{ strokeWidth: 1 }}
                isAnimationActive={false}
                content={<MetricTooltip series={series} format={format} total={stack} />}
              />
              {series.map((s) => {
                const color = `var(--color-${s.key})`;
                const last = lastIndex(rows, s.key);
                return (
                  <Area
                    key={s.key}
                    dataKey={s.key}
                    type="monotone"
                    stroke={color}
                    strokeWidth={2}
                    fill={lines ? "none" : `url(#${fade}-${s.key})`}
                    stackId={stack ? "stack" : undefined}
                    connectNulls={false}
                    isAnimationActive={false}
                    // Only the newest point gets a dot: where the series stands now.
                    dot={(p: { index: number; cx?: number; cy?: number }) =>
                      p.index === last && p.cx !== undefined && p.cy !== undefined ? (
                        <circle
                          key={`last-${s.key}`}
                          data-last-dot={s.key}
                          cx={p.cx}
                          cy={p.cy}
                          r={3}
                          fill={color}
                          stroke="var(--card)"
                          strokeWidth={2}
                        />
                      ) : (
                        <g key={`${s.key}-${p.index}`} />
                      )
                    }
                    activeDot={{ r: 4, fill: color, stroke: "var(--card)", strokeWidth: 2 }}
                  />
                );
              })}
            </AreaChart>
          </ChartContainer>
        )}
      </div>
      {/* A stack is labelled even with one app. */}
      {!message && (series.length > 1 || stacked) ? <MetricLegend series={series} /> : null}
    </ChartCardFrame>
  );
}

// ChartCardSkeleton is a chart card while its data loads: the title, four
// hairlines, a frosted ridge and tick stubs, so the grid does not jump when
// the chart arrives.
export function ChartCardSkeleton({
  title,
  height = 200,
  className,
}: {
  title?: string;
  height?: number;
  className?: string;
}) {
  return (
    <Card
      data-slot="widget-card"
      role="status"
      aria-label={title ? `Loading ${title}` : "Loading"}
      className={cn("flex min-w-0 flex-col gap-1.5 p-3.5", className)}
    >
      {title ? (
        <h3 className="truncate text-sm font-medium text-muted-foreground">{title}</h3>
      ) : (
        <div className="frost h-3.5 w-24 rounded" />
      )}
      <div className="relative flex flex-col justify-between py-2 pl-9" style={{ height }}>
        {[0, 1, 2, 3].map((i) => (
          <div key={i} className="frost h-px w-full opacity-80" />
        ))}
        <div
          className="frost absolute inset-y-2 left-9 right-0 opacity-70"
          style={{
            clipPath:
              "polygon(0 62%, 9% 48%, 18% 54%, 29% 34%, 40% 42%, 51% 22%, 62% 38%, 72% 30%, 83% 46%, 92% 26%, 100% 34%, 100% 100%, 0 100%)",
          }}
        />
        {[0, 1, 2].map((i) => (
          <div key={i} className="frost absolute left-0 h-2.5 w-6 rounded" style={{ top: `${8 + i * 33}%` }} />
        ))}
      </div>
    </Card>
  );
}
