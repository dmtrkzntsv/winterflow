import { useMemo, useState } from "react";

import { useAppBreadcrumbs } from "@/layouts/use-app-layout";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { MetricChartCard, ModeToggle, RangeSelect, type ChartSeries } from "@/components/metric-chart";
import { useApps } from "@/context/use-apps";
import { useServers } from "@/context/use-servers";
import { useMetricsOverview, useServerMetrics } from "@/hooks/use-metrics";
import {
  chartColor,
  dockerDiskUsage,
  formatPercent,
  formatRate,
  formatSize,
  formatTemp,
  stackApps,
  temperatureChart,
  toChartRows,
  type MetricPoint,
  type MetricsMode,
  type MetricsRange,
} from "@/lib/metrics";

// withPercents adds memory and swap as percentages of their totals (avg and
// peak), so both chart on one 0-100 axis.
function withPercents(points: MetricPoint[]): MetricPoint[] {
  return points.map((p) => {
    const out = { ...p };
    if (p.mem_total) {
      out.mem_pct = (100 * p.mem_used) / p.mem_total;
      out.mem_pct_max = (100 * (p.mem_used_max ?? p.mem_used)) / p.mem_total;
    }
    if (p.swap_total) {
      out.swap_pct = (100 * p.swap_used) / p.swap_total;
      out.swap_pct_max = (100 * (p.swap_used_max ?? p.swap_used)) / p.swap_total;
    }
    return out;
  });
}

function s(key: string, label: string, i: number): ChartSeries {
  return { key, label, color: chartColor(i) };
}

// MonitoringPage charts the switcher's active server: host resources, the
// per-app CPU and memory stack, and Docker's disk usage.
export default function MonitoringPage() {
  const { activeServer, activeServerId } = useServers();
  const { apps } = useApps();
  // 1h is served from raw samples, so it has data from the first minute;
  // coarser ranges fill in once their rollup buckets close.
  const [range, setRange] = useState<MetricsRange>("1h");
  const [mode, setMode] = useState<MetricsMode>("avg");
  const { data, loading, error } = useServerMetrics(activeServerId, range);
  const { data: overview } = useMetricsOverview();

  const breadcrumbs = useMemo(
    () => [{ label: "Monitoring", href: "#", hideOnMobile: true }, { label: activeServer?.name ?? "Server" }],
    [activeServer?.name],
  );
  useAppBreadcrumbs(breadcrumbs);

  const points = useMemo(() => withPercents(data?.points ?? []), [data]);
  const interval = data?.interval_seconds ?? 60;
  const rows = (fields: string[]) => toChartRows(points, interval, fields, mode);
  const temps = temperatureChart(points, interval, mode);
  // An app without a name (not reconciled yet) is labelled by its id prefix.
  const appName = (id: string) => apps.find((a) => a.id === id)?.name || id.slice(0, 8);
  const appSeries = (data?.apps ?? []).map((a, i) => s(a.app_id, appName(a.app_id), i));
  // The disk scan is hourly: take the latest sample's, not the range's.
  const latest = overview?.servers?.find((o) => o.server_id === activeServerId)?.latest?.server;
  const df = dockerDiskUsage(latest?.docker_df, points);
  // Peaks of different apps do not happen at once: summing them overstates
  // the total, so in peak mode the apps are drawn as separate lines.
  const peak = mode === "peak";

  if (!activeServerId) {
    return <p className="text-sm text-muted-foreground">No server selected.</p>;
  }

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h1 className="text-2xl font-semibold tracking-tight">{activeServer?.name ?? "Server"}</h1>
        <div className="flex flex-wrap gap-2">
          <RangeSelect value={range} onChange={setRange} />
          <ModeToggle value={mode} onChange={setMode} />
        </div>
      </div>
      {error ? <p className="text-sm text-destructive">{error}</p> : null}
      {loading && !data ? <p className="text-sm text-muted-foreground">Loading…</p> : null}

      <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
        <MetricChartCard title="CPU" rows={rows(["cpu"])} series={[s("cpu", "CPU", 0)]} format={formatPercent} range={range} />
        <MetricChartCard
          title="Memory"
          rows={rows(["mem_pct", "swap_pct"])}
          series={[s("mem_pct", "Memory", 1), s("swap_pct", "Swap", 3)]}
          format={formatPercent}
          range={range}
        />
        <MetricChartCard
          title="Load"
          rows={rows(["load1", "load5", "load15"])}
          series={[s("load1", "1 min", 0), s("load5", "5 min", 1), s("load15", "15 min", 2)]}
          format={(v) => v.toFixed(2)}
          range={range}
          lines
        />
        <MetricChartCard
          title="Disk usage"
          description="Fullest filesystem"
          rows={rows(["disk_pct"])}
          series={[s("disk_pct", "Used", 2)]}
          format={formatPercent}
          range={range}
        />
        <MetricChartCard
          title="Disk I/O"
          rows={rows(["disk_read_bps", "disk_write_bps"])}
          series={[s("disk_read_bps", "Read", 0), s("disk_write_bps", "Write", 1)]}
          format={formatRate}
          range={range}
        />
        <MetricChartCard
          title="Network"
          rows={rows(["net_rx_bps", "net_tx_bps"])}
          series={[s("net_rx_bps", "In", 1), s("net_tx_bps", "Out", 3)]}
          format={formatRate}
          range={range}
        />
        {/* No temperature sensors: the chart is hidden, not empty. */}
        {temps.series.length > 0 ? (
          <MetricChartCard
            title="Temperature"
            rows={temps.rows}
            series={temps.series}
            format={formatTemp}
            range={range}
            lines
          />
        ) : null}
        <MetricChartCard
          title="CPU by app"
          rows={stackApps(data?.apps ?? [], "cpu", mode)}
          series={appSeries}
          format={formatPercent}
          range={range}
          stacked={!peak}
          lines={peak}
        />
        <MetricChartCard
          title="Memory by app"
          rows={stackApps(data?.apps ?? [], "mem_used", mode)}
          series={appSeries}
          format={formatSize}
          range={range}
          stacked={!peak}
          lines={peak}
        />
      </div>

      <Card>
        <CardHeader className="pb-2">
          <CardTitle className="text-sm font-medium">Docker disk usage</CardTitle>
        </CardHeader>
        <CardContent>
          {df ? (
            <dl className="grid grid-cols-2 gap-3 text-sm md:grid-cols-4" data-testid="docker-df">
              <div>
                <dt className="text-muted-foreground">Images</dt>
                <dd>{formatSize(df.images)}</dd>
              </div>
              <div>
                <dt className="text-muted-foreground">Volumes</dt>
                <dd>{formatSize(df.volumes)}</dd>
              </div>
              <div>
                <dt className="text-muted-foreground">Build cache</dt>
                <dd>{formatSize(df.build_cache)}</dd>
              </div>
              <div>
                <dt className="text-muted-foreground">Reclaimable</dt>
                <dd>{formatSize(df.reclaimable)}</dd>
              </div>
            </dl>
          ) : (
            <p className="text-sm text-muted-foreground">
              No disk scan yet — it runs hourly (METRICS_DISK_INTERVAL).
            </p>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
