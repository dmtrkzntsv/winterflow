import { useMemo, useState } from "react";
import { HardDrive } from "lucide-react";

import { useAppBreadcrumbs } from "@/layouts/use-app-layout";
import { Card } from "@/components/ui/card";
import { ChartEmpty } from "@/components/chart-parts";
import { DataFreshness } from "@/components/data-freshness";
import { KpiTile, KpiTileSkeleton } from "@/components/kpi-tile";
import { ChartCardSkeleton, MetricChartCard, type ChartSeries } from "@/components/metric-chart";
import { RangeMenu } from "@/components/range-menu";
import { useApps } from "@/context/use-apps";
import { useServers } from "@/context/use-servers";
import { useServerPath } from "@/context/use-server-path";
import { useMetricsOverview, useServerMetrics } from "@/hooks/use-metrics";
import { appLabel, deltaLabel, isSignificant, kpiNow, latestValue, percentCeiling, periodDelta, withNetTotal } from "@/lib/kpi";
import {
  chartColor,
  dockerDiskUsage,
  formatPercent,
  formatRate,
  formatSize,
  formatTemp,
  fullestDisk,
  lastPoint,
  stackApps,
  temperatureChart,
  toChartRows,
  type ChartRow,
  type DockerDF,
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

const KPI_GRID = "grid grid-cols-2 gap-3 lg:grid-cols-4";
// Fixed rows with plots that fill them: cards in a row end level whether
// or not they carry a legend.
const CHART_GRID = "grid auto-rows-[18.5rem] gap-3 md:grid-cols-2";
const BIG = 240;

// DockerDisk is the hourly Docker disk scan: one proportion bar of images,
// volumes and build cache, then the figures, reclaimable last.
function DockerDisk({ df }: { df: DockerDF | undefined }) {
  const parts = df
    ? [
        { label: "Images", value: df.images, color: chartColor(0) },
        { label: "Volumes", value: df.volumes, color: chartColor(1) },
        { label: "Build cache", value: df.build_cache, color: chartColor(2) },
      ]
    : [];
  const total = parts.reduce((a, p) => a + p.value, 0);
  return (
    <Card data-slot="widget-card" className="flex min-w-0 flex-col gap-3 p-3.5">
      <div className="flex min-h-5 items-center gap-2">
        <h3 className="truncate text-sm font-medium text-muted-foreground">Docker disk usage</h3>
        {df ? (
          <span className="ml-auto shrink-0 text-xs text-muted-foreground tabular-nums">{formatSize(total)} total</span>
        ) : null}
      </div>
      {df ? (
        <>
          {total > 0 ? (
            <div className="flex h-2 gap-0.5 overflow-hidden rounded-full bg-muted" aria-hidden>
              {parts.map((p) =>
                p.value > 0 ? (
                  <div key={p.label} style={{ width: `${(100 * p.value) / total}%`, backgroundColor: p.color }} />
                ) : null,
              )}
            </div>
          ) : null}
          <dl className="grid grid-cols-2 gap-x-3 gap-y-3 sm:grid-cols-4" data-testid="docker-df">
            {parts.map((p) => (
              <div key={p.label} className="min-w-0">
                <dt className="flex items-center gap-1.5 text-xs text-muted-foreground">
                  <span aria-hidden className="size-2 shrink-0 rounded-full" style={{ backgroundColor: p.color }} />
                  {p.label}
                </dt>
                <dd className="mt-1 text-xl font-semibold tracking-tight tabular-nums">{formatSize(p.value)}</dd>
              </div>
            ))}
            <div className="min-w-0">
              <dt className="text-xs text-muted-foreground">Reclaimable</dt>
              <dd className="mt-1 text-xl font-semibold tracking-tight text-muted-foreground tabular-nums">
                {formatSize(df.reclaimable)}
              </dd>
            </div>
          </dl>
        </>
      ) : (
        <ChartEmpty
          icon={HardDrive}
          className="py-6"
          message="No disk scan yet — it runs hourly (METRICS_DISK_INTERVAL)."
        />
      )}
    </Card>
  );
}

// MonitoringPage charts the switcher's active server: a KPI row with the
// range's trend, a large CPU chart, the other host resources, the per-app
// CPU and memory stack, and Docker's disk usage.
export default function MonitoringPage() {
  const { activeServer, activeServerId } = useServers();
  const toServer = useServerPath();
  const { apps } = useApps();
  // 1h is served from raw samples, so it has data from the first minute;
  // coarser ranges fill in once their rollup buckets close.
  const [range, setRange] = useState<MetricsRange>("1h");
  const [mode, setMode] = useState<MetricsMode>("avg");
  const { data, loading, error, updatedAt, refresh } = useServerMetrics(activeServerId, range);
  const { data: overview, refresh: refreshOverview } = useMetricsOverview();

  const breadcrumbs = useMemo(
    () => [
      { label: "Monitoring", href: toServer("/monitoring"), hideOnMobile: true },
      { label: activeServer?.name ?? "Server" },
    ],
    [activeServer?.name, toServer],
  );
  useAppBreadcrumbs(breadcrumbs);

  const points = useMemo(() => withNetTotal(withPercents(data?.points ?? [])), [data]);
  const interval = data?.interval_seconds ?? 60;
  const rows = (fields: string[]) => toChartRows(points, interval, fields, mode);
  const temps = temperatureChart(points, interval, mode);
  const appSeries = (data?.apps ?? []).map((a, i) =>
    s(a.app_id, appLabel(apps.find((x) => x.id === a.app_id)?.name, a.app_id), i),
  );
  // The disk scan is hourly: take the latest sample's, not the range's.
  const latest = overview?.servers?.find((o) => o.server_id === activeServerId)?.latest?.server;
  const df = dockerDiskUsage(latest?.docker_df, points);
  // Peaks of different apps do not happen at once: summing them overstates
  // the total, so in peak mode the apps are drawn as separate lines.
  const peak = mode === "peak";

  if (!activeServerId) {
    return <p className="text-sm text-muted-foreground">No server selected.</p>;
  }

  const initial = loading && !data;
  const last = lastPoint(points);
  const disk = fullestDisk(latest?.disks);
  const cpuRows = rows(["cpu", "load1"]);
  const memRows = rows(["mem_pct"]);
  const diskRows = rows(["disk_pct"]);
  const netRows = rows(["net_bps", "net_rx_bps", "net_tx_bps"]);
  // Headline figures are live (the overview's latest sample) on every range;
  // the sparkline and the delta describe the range.
  const now = kpiNow(latest, {
    cpu: latestValue(cpuRows, "cpu"),
    mem_pct: latestValue(memRows, "mem_pct"),
    disk_pct: latestValue(diskRows, "disk_pct"),
    net_bps: latestValue(netRows, "net_bps"),
    net_rx_bps: latestValue(netRows, "net_rx_bps"),
    net_tx_bps: latestValue(netRows, "net_tx_bps"),
    load1: latestValue(cpuRows, "load1"),
  });
  const vs = deltaLabel(range);
  const delta = (r: ChartRow[], field: string, unit: "percent" | "amount") => {
    const d = periodDelta(r, field, range, interval);
    return { delta: d?.percent ?? null, significant: d ? isSignificant(d, unit) : true };
  };
  const memTotal = latest?.mem_total ?? last?.mem_total;
  const memUsed = latest?.mem_total ? latest.mem_used : last?.mem_used;

  return (
    <div className="space-y-3">
      <header className="flex flex-wrap items-end justify-between gap-x-4 gap-y-3 pb-3">
        <div className="min-w-0">
          <h1 className="truncate text-2xl font-semibold tracking-tight">{activeServer?.name ?? "Server"}</h1>
          <DataFreshness
            className="mt-1"
            updatedAt={updatedAt}
            onRefresh={() => Promise.all([refresh(), refreshOverview()])}
          />
        </div>
        <RangeMenu range={range} onRangeChange={setRange} mode={mode} onModeChange={setMode} />
      </header>
      {error ? <p className="text-sm text-destructive">{error}</p> : null}

      {initial ? (
        <>
          <div className={KPI_GRID}>
            {["CPU", "Memory", "Disk", "Network"].map((l) => (
              <KpiTileSkeleton key={l} label={l} />
            ))}
          </div>
          <ChartCardSkeleton title="CPU" height={BIG} />
          <div className={CHART_GRID}>
            {["Memory", "Load", "Disk usage", "Disk I/O"].map((t) => (
              <ChartCardSkeleton key={t} title={t} className="h-full" />
            ))}
          </div>
        </>
      ) : (
        <>
          {/* Percent sparks run from zero to a round ceiling: a disk at 56.4 %
              wobbling by 0.2 % must not draw as a cliff. */}
          <div className={KPI_GRID}>
            <KpiTile
              label="CPU"
              value={formatPercent(now.cpu)}
              {...delta(cpuRows, "cpu", "percent")}
              deltaLabel={vs}
              goodWhen="down"
              sub={now.load1 !== undefined ? `Load ${now.load1.toFixed(2)}` : undefined}
              spark={{ rows: cpuRows, field: "cpu", color: chartColor(0), max: percentCeiling(cpuRows, "cpu") }}
            />
            <KpiTile
              label="Memory"
              value={formatPercent(now.mem_pct)}
              {...delta(memRows, "mem_pct", "percent")}
              deltaLabel={vs}
              goodWhen="down"
              sub={memTotal ? `${formatSize(memUsed)} of ${formatSize(memTotal)}` : undefined}
              spark={{ rows: memRows, field: "mem_pct", color: chartColor(1), max: percentCeiling(memRows, "mem_pct") }}
            />
            <KpiTile
              label="Disk"
              value={formatPercent(now.disk_pct)}
              {...delta(diskRows, "disk_pct", "percent")}
              deltaLabel={vs}
              goodWhen="down"
              sub={disk ? `${formatSize(disk.used)} of ${formatSize(disk.total)} · ${disk.mount}` : undefined}
              spark={{ rows: diskRows, field: "disk_pct", color: chartColor(2), max: percentCeiling(diskRows, "disk_pct") }}
            />
            <KpiTile
              label="Network"
              value={formatRate(now.net_bps)}
              {...delta(netRows, "net_bps", "amount")}
              deltaLabel={vs}
              goodWhen="neutral"
              sub={
                now.net_bps !== undefined
                  ? `In ${formatRate(now.net_rx_bps)} · Out ${formatRate(now.net_tx_bps)}`
                  : undefined
              }
              spark={{ rows: netRows, field: "net_bps", color: chartColor(4) }}
            />
          </div>

          <MetricChartCard
            title="CPU"
            rows={rows(["cpu"])}
            series={[s("cpu", "CPU", 0)]}
            format={formatPercent}
            range={range}
            height={BIG}
          />

          <div className={CHART_GRID}>
            <MetricChartCard
              height="fill"
              title="Memory"
              rows={rows(["mem_pct", "swap_pct"])}
              series={[s("mem_pct", "Memory", 1), s("swap_pct", "Swap", 3)]}
              format={formatPercent}
              range={range}
            />
            <MetricChartCard
              height="fill"
              title="Load"
              rows={rows(["load1", "load5", "load15"])}
              series={[s("load1", "1 min", 0), s("load5", "5 min", 1), s("load15", "15 min", 2)]}
              format={(v) => v.toFixed(2)}
              scale="integer"
              range={range}
              lines
            />
            <MetricChartCard
              height="fill"
              title="Disk usage"
              description="Fullest filesystem"
              rows={diskRows}
              series={[s("disk_pct", "Used", 2)]}
              format={formatPercent}
              range={range}
            />
            <MetricChartCard
              height="fill"
              title="Disk I/O"
              rows={rows(["disk_read_bps", "disk_write_bps"])}
              series={[s("disk_read_bps", "Read", 0), s("disk_write_bps", "Write", 1)]}
              format={formatRate}
              range={range}
            />
            <MetricChartCard
              height="fill"
              title="Network"
              rows={rows(["net_rx_bps", "net_tx_bps"])}
              series={[s("net_rx_bps", "In", 1), s("net_tx_bps", "Out", 3)]}
              format={formatRate}
              range={range}
            />
            {/* No temperature sensors: the chart is hidden, not empty. */}
            {temps.series.length > 0 ? (
              <MetricChartCard
                height="fill"
                title="Temperature"
                rows={temps.rows}
                series={temps.series}
                format={formatTemp}
                range={range}
                lines
              />
            ) : null}
            <MetricChartCard
              height="fill"
              title="CPU by app"
              rows={stackApps(data?.apps ?? [], "cpu", mode)}
              series={appSeries}
              format={formatPercent}
              range={range}
              stacked={!peak}
              lines={peak}
            />
            <MetricChartCard
              height="fill"
              title="Memory by app"
              rows={stackApps(data?.apps ?? [], "mem_used", mode)}
              series={appSeries}
              format={formatSize}
              range={range}
              stacked={!peak}
              lines={peak}
              // Without the temperature chart the grid is odd: the last
              // card takes the full row instead of leaving a hole.
              className={temps.series.length > 0 ? undefined : "md:col-span-2"}
            />
          </div>
        </>
      )}

      <DockerDisk df={df} />
    </div>
  );
}
