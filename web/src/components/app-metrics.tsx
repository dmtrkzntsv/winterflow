import { useMemo, useState } from "react";

import { DataFreshness } from "@/components/data-freshness";
import { KpiTile, KpiTileSkeleton } from "@/components/kpi-tile";
import { ChartCardSkeleton, MetricChartCard } from "@/components/metric-chart";
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
import { latestValue, percentCeiling, withNetTotal } from "@/lib/kpi";

// Fixed rows with plots that fill them: cards in a row end level whether
// or not they carry a legend.
const CHART_GRID = "grid auto-rows-[18.5rem] gap-3 md:grid-cols-2";

// UNAVAILABLE is shown when the host's cgroups resolve under no known
// layout (rootless Docker, exotic drivers): the agent sends
// containers_unavailable and stores "unavailable": 1 (a rolled-up point
// carries the flagged fraction; see isUnavailable).
const UNAVAILABLE = "Container metrics are unavailable on this host";

const KPI_GRID = "grid grid-cols-2 gap-3 lg:grid-cols-4";

// AppMetricBlocks is the app header's KPI row: CPU now, memory against its
// limit, disk and network rate, each from the newest 1-minute point with
// the last hour as a sparkline.
export function AppMetricBlocks({ serverId, appId }: { serverId: string; appId: string }) {
  const { data, loading } = useAppMetrics(serverId, appId, "1h");
  const points = useMemo(() => withNetTotal(data?.points ?? []), [data]);
  if (loading && !data) {
    return (
      <div className={KPI_GRID}>
        {["CPU", "Memory", "Disk", "Network"].map((l) => (
          <KpiTileSkeleton key={l} label={l} />
        ))}
      </div>
    );
  }
  const last = lastPoint(points);
  if (!last) return null;
  const rows = toChartRows(
    points,
    data?.interval_seconds ?? 60,
    ["cpu", "mem_used", "disk", "net_bps", "net_rx_bps", "net_tx_bps"],
    "avg",
  );
  // A newest point may lack a field (rates need two samples): the newest
  // value that exists stands in, as on /monitoring.
  const now = (field: string) => latestValue(rows, field);
  // Container metrics come from cgroups; the disk figure comes from the
  // hourly scan, so it stays meaningful when cgroups are unavailable.
  const blind = isUnavailable(last);
  const hasDisk = last.disk !== undefined;
  if (blind && !hasDisk) {
    return <p className="text-sm text-muted-foreground">{UNAVAILABLE}</p>;
  }
  const spark = (field: string, i: number, max?: number) => ({ rows, field, color: chartColor(i), max });
  return (
    <div className="space-y-2">
      {blind ? <p className="text-sm text-muted-foreground">{UNAVAILABLE}</p> : null}
      <div className={KPI_GRID}>
        {blind ? null : (
          <>
            <KpiTile
              label="CPU"
              value={formatPercent(now("cpu"))}
              sub="of host"
              spark={spark("cpu", 0, percentCeiling(rows, "cpu"))}
            />
            <KpiTile
              label="Memory"
              value={formatSize(now("mem_used"))}
              sub={last.mem_limit ? `of ${formatSize(last.mem_limit)} limit` : "no limit"}
              // Against a limit the line reads as headroom: 0 to the limit.
              spark={spark("mem_used", 1, last.mem_limit || undefined)}
            />
          </>
        )}
        {hasDisk || !blind ? (
          <KpiTile
            label="Disk"
            value={formatSize(last.disk)}
            sub={last.image ? `+ ${formatSize(last.image)} images (may be shared)` : undefined}
            spark={spark("disk", 2)}
          />
        ) : null}
        {blind ? null : (
          <KpiTile
            label="Network"
            value={formatRate(now("net_bps"))}
            sub={
              now("net_bps") !== undefined
                ? `In ${formatRate(now("net_rx_bps"))} · Out ${formatRate(now("net_tx_bps"))}`
                : undefined
            }
            spark={spark("net_bps", 4)}
          />
        )}
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
  const { data, error, loading, updatedAt, refresh } = useAppMetrics(serverId, appId, range);
  const points = useMemo(() => data?.points ?? [], [data]);
  const interval = data?.interval_seconds ?? 60;
  const rows = (fields: string[]) => toChartRows(points, interval, fields, mode);
  const blind = isUnavailable(lastPoint(points)) ? UNAVAILABLE : undefined;
  // No container of the app has a limit: no "Limit" series to show.
  const memSeries = [{ key: "mem_used", label: "Used", color: chartColor(1) }];
  if (hasData(points, ["mem_limit"])) memSeries.push({ key: "mem_limit", label: "Limit", color: chartColor(3) });

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2">
        <DataFreshness updatedAt={updatedAt} onRefresh={refresh} />
        <RangeMenu range={range} onRangeChange={setRange} mode={mode} onModeChange={setMode} />
      </div>
      {error ? <p className="text-sm text-destructive">{error}</p> : null}
      {loading && !data ? (
        <div className={CHART_GRID}>
          {["CPU", "Memory", "Network", "Disk"].map((t) => (
            <ChartCardSkeleton key={t} title={t} className="h-full" />
          ))}
        </div>
      ) : (
        <div className={CHART_GRID}>
          <MetricChartCard
            height="fill"
            title="CPU"
            description="Percent of the whole host"
            rows={rows(["cpu"])}
            series={[{ key: "cpu", label: "CPU", color: chartColor(0) }]}
            format={formatPercent}
            range={range}
            empty={blind}
          />
          <MetricChartCard
            height="fill"
            title="Memory"
            rows={rows(memSeries.map((m) => m.key))}
            series={memSeries}
            format={formatSize}
            range={range}
            empty={blind}
          />
          <MetricChartCard
            height="fill"
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
            height="fill"
            title="Disk"
            description="Volumes, app directory and writable layers (hourly)"
            rows={rows(["disk"])}
            series={[{ key: "disk", label: "Disk", color: chartColor(2) }]}
            format={formatSize}
            range={range}
          />
        </div>
      )}
    </div>
  );
}
