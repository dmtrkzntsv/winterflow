import { useMemo, useState } from "react";
import { HardDrive, Server as ServerIcon } from "lucide-react";

import { FiringBanner } from "@/components/alerts/firing-banner";
import { TargetAlertsCard } from "@/components/alerts/target-alerts-card";
import { ChartEmpty } from "@/components/chart-parts";
import { DataFreshness } from "@/components/data-freshness";
import { Disclosure } from "@/components/disclosure";
import { EmptyState } from "@/components/empty-state";
import { KpiTile, KpiTileSkeleton } from "@/components/kpi-tile";
import { ChartCardSkeleton, MetricChartCard, type ChartSeries } from "@/components/metric-chart";
import { ProcessesTab } from "@/components/monitoring/processes-tab";
import { PageHeader } from "@/components/page-header";
import { RangeMenu } from "@/components/range-menu";
import { Section } from "@/components/section";
import { Card } from "@/components/ui/card";
import { TabsContent } from "@/components/ui/tabs";
import { UnderlineTabs } from "@/components/underline-tabs";
import { useApps } from "@/context/use-apps";
import { useServers } from "@/context/use-servers";
import { useMetricsOverview, useServerMetrics } from "@/hooks/use-metrics";
import { useTabParam } from "@/hooks/use-tab-param";
import { appLabel, kpiNow, latestValue, withNetTotal } from "@/lib/kpi";
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

// Two compact tiles per row on a phone, four from lg.
const TABS = ["overview", "processes"] as const;
type Tab = (typeof TABS)[number];

const KPI_GRID = "grid grid-cols-2 gap-3 lg:grid-cols-4";
// One chart per row below md, two from md. Fixed rows with plots that fill
// them: cards in a row end level whether or not they carry a legend.
const CHART_GRID = "grid auto-rows-[18.5rem] gap-3 md:grid-cols-2";
const HOST_CHARTS = ["CPU", "Memory", "Disk usage", "Network"];

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
        <ChartEmpty icon={HardDrive} className="py-6" message="No disk scan yet. It runs once an hour." />
      )}
    </Card>
  );
}

// MonitoringPage shows the active server in two tabs (spec §5), the firing
// alerts on top of both. Overview: four KPI tiles, the four host charts,
// what each app uses, then Load, Disk I/O, Temperature and Docker's disk
// usage under "More details" (not drawn until opened), and the server's
// alert rules collapsed. Processes: the live process list, polled only
// while that tab is open.
export default function MonitoringPage() {
  const { activeServer, activeServerId } = useServers();
  const { apps } = useApps();
  // 1h is served from raw samples, so it has data from the first minute;
  // coarser ranges fill in once their rollup buckets close.
  const [range, setRange] = useState<MetricsRange>("1h");
  const [mode, setMode] = useState<MetricsMode>("avg");
  const [tab, setTab] = useTabParam<Tab>(TABS, "overview");
  const { data, loading, error, updatedAt, refresh } = useServerMetrics(activeServerId, range);
  const { data: overview, refresh: refreshOverview } = useMetricsOverview();

  const points = useMemo(() => withNetTotal(withPercents(data?.points ?? [])), [data]);

  if (!activeServerId) {
    return (
      <div className="space-y-6">
        <PageHeader title="Monitoring" />
        <EmptyState icon={ServerIcon} title="No server selected." />
      </div>
    );
  }

  const serverName = activeServer?.name ?? "Server";
  const interval = data?.interval_seconds ?? 60;
  const rows = (fields: string[]) => toChartRows(points, interval, fields, mode);
  const temps = temperatureChart(points, interval, mode);
  const appSeries = (data?.apps ?? []).map((a, i) =>
    s(a.app_id, appLabel(apps.find((x) => x.id === a.app_id)?.name), i),
  );
  // The disk scan is hourly: take the latest sample's, not the range's.
  const latest = overview?.servers?.find((o) => o.server_id === activeServerId)?.latest?.server;
  const df = dockerDiskUsage(latest?.docker_df, points);
  // Peaks of different apps do not happen at once: summing them overstates
  // the total, so in peak mode the apps are drawn as separate lines.
  const peak = mode === "peak";

  const initial = loading && !data;
  const last = lastPoint(points);
  const disk = fullestDisk(latest?.disks);
  const cpuRows = rows(["cpu", "load1"]);
  const memRows = rows(["mem_pct"]);
  const diskRows = rows(["disk_pct"]);
  const netRows = rows(["net_bps", "net_rx_bps", "net_tx_bps"]);
  // Headline figures are live (the overview's latest sample) on every range;
  // the charts below carry the trend.
  const now = kpiNow(latest, {
    cpu: latestValue(cpuRows, "cpu"),
    mem_pct: latestValue(memRows, "mem_pct"),
    disk_pct: latestValue(diskRows, "disk_pct"),
    net_bps: latestValue(netRows, "net_bps"),
    net_rx_bps: latestValue(netRows, "net_rx_bps"),
    net_tx_bps: latestValue(netRows, "net_tx_bps"),
    load1: latestValue(cpuRows, "load1"),
  });
  const memTotal = latest?.mem_total ?? last?.mem_total;
  const memUsed = latest?.mem_total ? latest.mem_used : last?.mem_used;

  return (
    <div className="space-y-6">
      <PageHeader
        title="Monitoring"
        subtitle={
          <span className="inline-flex flex-wrap items-center gap-x-1.5">
            <span className="font-medium text-foreground">{serverName}</span>
            {tab === "overview" ? (
              <>
                <span aria-hidden>·</span>
                <DataFreshness updatedAt={updatedAt} onRefresh={() => Promise.all([refresh(), refreshOverview()])} />
              </>
            ) : null}
          </span>
        }
        actions={
          tab === "overview" ? (
            <RangeMenu range={range} onRangeChange={setRange} mode={mode} onModeChange={setMode} />
          ) : undefined
        }
      />
      <FiringBanner serverId={activeServerId} />
      <UnderlineTabs<Tab>
        value={tab}
        onValueChange={setTab}
        label="Monitoring views"
        items={[
          { value: "overview", label: "Overview" },
          { value: "processes", label: "Processes" },
        ]}
      >
        <TabsContent value="overview" className="mt-6 space-y-6">
          {error ? <p className="text-sm text-destructive">{error}</p> : null}

          {initial ? (
            <>
              <div className={KPI_GRID}>
                {["CPU", "Memory", "Disk", "Network"].map((l) => (
                  <KpiTileSkeleton key={l} label={l} />
                ))}
              </div>
              <div className={CHART_GRID}>
                {HOST_CHARTS.map((t) => (
                  <ChartCardSkeleton key={t} title={t} className="h-full" />
                ))}
              </div>
            </>
          ) : (
            <>
              <div className={KPI_GRID} data-testid="kpi-grid">
                <KpiTile
                  label="CPU"
                  value={formatPercent(now.cpu)}
                  sub={now.load1 !== undefined ? `Load ${now.load1.toFixed(2)}` : undefined}
                />
                <KpiTile
                  label="Memory"
                  value={formatPercent(now.mem_pct)}
                  sub={memTotal ? `${formatSize(memUsed)} of ${formatSize(memTotal)}` : undefined}
                />
                <KpiTile
                  label="Disk"
                  value={formatPercent(now.disk_pct)}
                  sub={
                    disk ? (
                      <>
                        {formatSize(disk.used)} of {formatSize(disk.total)}
                        {disk.mount ? <span className="text-muted-foreground/70"> · {disk.mount}</span> : null}
                      </>
                    ) : undefined
                  }
                />
                <KpiTile
                  label="Network"
                  value={formatRate(now.net_bps)}
                  sub={
                    now.net_bps !== undefined
                      ? `In ${formatRate(now.net_rx_bps)} · Out ${formatRate(now.net_tx_bps)}`
                      : undefined
                  }
                />
              </div>

              <div className={CHART_GRID} data-testid="host-charts">
                <MetricChartCard
                  height="fill"
                  title="CPU"
                  rows={rows(["cpu"])}
                  series={[s("cpu", "CPU", 0)]}
                  format={formatPercent}
                  range={range}
                />
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
                  title="Disk usage"
                  description="Fullest filesystem"
                  rows={diskRows}
                  series={[s("disk_pct", "Used", 2)]}
                  format={formatPercent}
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
              </div>

              <Section title="Apps">
                <div className={CHART_GRID} data-testid="app-charts">
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
                  />
                </div>
              </Section>

              {/* Collapsed by default: these charts are not drawn until opened. */}
              <Disclosure label="More details">
                <div className={CHART_GRID}>
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
                    title="Disk I/O"
                    rows={rows(["disk_read_bps", "disk_write_bps"])}
                    series={[s("disk_read_bps", "Read", 0), s("disk_write_bps", "Write", 1)]}
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
                      className="md:col-span-2"
                    />
                  ) : null}
                </div>
                <DockerDisk df={df} />
              </Disclosure>
            </>
          )}

          <TargetAlertsCard serverId={activeServerId} targetLabel={activeServer?.name ?? "this server"} />
        </TabsContent>
        <TabsContent value="processes" className="mt-6">
          {tab === "processes" ? <ProcessesTab serverId={activeServerId} /> : null}
        </TabsContent>
      </UnderlineTabs>
    </div>
  );
}
