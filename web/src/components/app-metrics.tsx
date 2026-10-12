import { useMemo, useState } from "react";

import { Card, CardContent } from "@/components/ui/card";
import { MetricChartCard } from "@/components/metric-chart";
import { RangeMenu } from "@/components/range-menu";
import { useAppMetrics } from "@/hooks/use-metrics";
import {
  chartColor,
  formatPercent,
  formatRate,
  formatSize,
  hasData,
  isUnavailable,
  lastPoint,
  toChartRows,
  type MetricsMode,
  type MetricsRange,
} from "@/lib/metrics";

// UNAVAILABLE is shown when the host's cgroups resolve under no known
// layout (rootless Docker, exotic drivers): the agent sends
// containers_unavailable and stores "unavailable": 1 (a rolled-up point
// carries the flagged fraction; see isUnavailable).
const UNAVAILABLE = "Container metrics are unavailable on this host";

function Block({ label, value, detail }: { label: string; value: string; detail?: string }) {
  return (
    <Card className="min-w-[150px] flex-1" data-testid={`block-${label}`}>
      <CardContent className="p-3">
        <div className="text-xs text-muted-foreground">{label}</div>
        <div className="text-lg font-semibold tabular-nums">{value}</div>
        {detail ? <div className="text-xs text-muted-foreground">{detail}</div> : null}
      </CardContent>
    </Card>
  );
}

// AppMetricBlocks is the app header's row of info blocks: CPU now, memory
// against its limit, disk and network rate, from the newest 1-minute point.
export function AppMetricBlocks({ serverId, appId }: { serverId: string; appId: string }) {
  const { data } = useAppMetrics(serverId, appId, "1h");
  const last = lastPoint(data?.points ?? []);
  if (!last) return null;
  // Container metrics come from cgroups; the disk figure comes from the
  // hourly scan, so it stays meaningful when cgroups are unavailable.
  const blind = isUnavailable(last);
  const net =
    last.net_rx_bps !== undefined || last.net_tx_bps !== undefined
      ? (last.net_rx_bps ?? 0) + (last.net_tx_bps ?? 0)
      : undefined;
  const hasDisk = last.disk !== undefined;
  if (blind && !hasDisk) {
    return <p className="text-sm text-muted-foreground">{UNAVAILABLE}</p>;
  }
  return (
    <div className="space-y-2">
      {blind ? <p className="text-sm text-muted-foreground">{UNAVAILABLE}</p> : null}
      <div className="flex flex-wrap gap-3">
        {blind ? null : (
          <>
            <Block label="CPU" value={formatPercent(last.cpu)} detail="of host" />
            <Block
              label="Memory"
              value={formatSize(last.mem_used)}
              detail={last.mem_limit ? `of ${formatSize(last.mem_limit)} limit` : "no limit"}
            />
          </>
        )}
        {hasDisk || !blind ? (
          <Block
            label="Disk"
            value={formatSize(last.disk)}
            detail={last.image ? `+ ${formatSize(last.image)} images (may be shared)` : undefined}
          />
        ) : null}
        {blind ? null : <Block label="Network" value={formatRate(net)} />}
      </div>
    </div>
  );
}

// AppMetricsTab is the app page's Metrics tab: CPU, memory, network and
// disk growth over the selected range.
export function AppMetricsTab({ serverId, appId }: { serverId: string; appId: string }) {
  // 1h has data from the first minute (raw samples); see MonitoringPage.
  const [range, setRange] = useState<MetricsRange>("1h");
  const [mode, setMode] = useState<MetricsMode>("avg");
  const { data, error } = useAppMetrics(serverId, appId, range);
  const points = useMemo(() => data?.points ?? [], [data]);
  const interval = data?.interval_seconds ?? 60;
  const rows = (fields: string[]) => toChartRows(points, interval, fields, mode);
  const blind = isUnavailable(lastPoint(points)) ? UNAVAILABLE : undefined;
  // No container of the app has a limit: no "Limit" series to show.
  const memSeries = [{ key: "mem_used", label: "Used", color: chartColor(1) }];
  if (hasData(points, ["mem_limit"])) memSeries.push({ key: "mem_limit", label: "Limit", color: chartColor(3) });

  return (
    <div className="space-y-4">
      <RangeMenu className="justify-end" range={range} onRangeChange={setRange} mode={mode} onModeChange={setMode} />
      {error ? <p className="text-sm text-destructive">{error}</p> : null}
      <div className="grid gap-4 md:grid-cols-2">
        <MetricChartCard
          title="CPU"
          description="Percent of the whole host"
          rows={rows(["cpu"])}
          series={[{ key: "cpu", label: "CPU", color: chartColor(0) }]}
          format={formatPercent}
          range={range}
          empty={blind}
        />
        <MetricChartCard
          title="Memory"
          rows={rows(memSeries.map((m) => m.key))}
          series={memSeries}
          format={formatSize}
          range={range}
          empty={blind}
        />
        <MetricChartCard
          title="Network"
          rows={rows(["net_rx_bps", "net_tx_bps"])}
          series={[
            { key: "net_rx_bps", label: "In", color: chartColor(1) },
            { key: "net_tx_bps", label: "Out", color: chartColor(3) },
          ]}
          format={formatRate}
          range={range}
          empty={blind}
        />
        <MetricChartCard
          title="Disk"
          description="Volumes, app directory and writable layers (hourly)"
          rows={rows(["disk"])}
          series={[{ key: "disk", label: "Disk", color: chartColor(2) }]}
          format={formatSize}
          range={range}
        />
      </div>
    </div>
  );
}
