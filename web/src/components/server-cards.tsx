import { useMemo } from "react";
import { useNavigate } from "react-router-dom";

import { Card } from "@/components/ui/card";
import { Sparkline } from "@/components/sparkline";
import { useServers } from "@/context/use-servers";
import type { Server } from "@/context/servers-context-base";
import { useMetricsOverview } from "@/hooks/use-metrics";
import { useNow } from "@/hooks/use-now";
import { latestValue, percentCeiling, serverPresence, type Presence } from "@/lib/kpi";
import {
  chartColor,
  formatPercent,
  formatTemp,
  fullestDisk,
  percentOf,
  toChartRows,
  type ServerOverview,
} from "@/lib/metrics";
import { cn, formatBytes } from "@/lib/utils";

const SPARK_INTERVAL = 60; // the overview sparkline is tier 1: one point a minute

const DOT: Record<Presence["tone"], string> = {
  live: "bg-emerald-500",
  offline: "bg-muted-foreground/40",
  waiting: "bg-muted-foreground/25",
};

function specsLine(caps: Record<string, string>): string[] {
  const parts: string[] = [];
  if (caps.system_cpu_cores) parts.push(`${caps.system_cpu_cores} cores`);
  const mem = formatBytes(caps.system_memory_total);
  if (mem) parts.push(`${mem} RAM`);
  const disk = formatBytes(caps.system_disk_total);
  if (disk) parts.push(`${disk} disk`);
  return parts;
}

// Meter is one slim usage bar: label, a thin bar in the series colour (red
// past 90 %), the percentage. Without a figure the bar stays empty and the
// value reads "–", never a fake 0 %.
function Meter({
  label,
  percent,
  color,
  title,
}: {
  label: string;
  percent: number | undefined;
  color: string;
  title?: string;
}) {
  const width = percent === undefined ? 0 : Math.min(100, Math.max(0, percent));
  return (
    <div
      className="grid grid-cols-[3.75rem_1fr_2.75rem] items-center gap-2 text-xs"
      data-testid={`meter-${label}`}
      title={title}
    >
      <span className="text-muted-foreground">{label}</span>
      <div className="h-1.5 overflow-hidden rounded-full bg-muted" aria-hidden>
        <div
          className="h-full rounded-full"
          style={{ width: `${width}%`, backgroundColor: width >= 90 ? "var(--destructive)" : color }}
        />
      </div>
      <span className="text-right font-medium tabular-nums">{formatPercent(percent)}</span>
    </div>
  );
}

// HealthCard is one server's dashboard card in twillingate's project-card
// shape: name and a presence dot, the last hour of CPU as one sparkline,
// slim usage bars, then a meta line and a quieter specs line. The whole
// card opens the server's Monitoring page.
function HealthCard({
  server,
  overview,
  online,
  now,
  onOpen,
}: {
  server: Server;
  overview: ServerOverview | undefined;
  online: boolean;
  now: number;
  onOpen: () => void;
}) {
  const latest = overview?.latest?.server;
  const rows = useMemo(
    () => toChartRows(overview?.sparkline ?? [], SPARK_INTERVAL, ["cpu"], "avg"),
    [overview?.sparkline],
  );
  const disk = fullestDisk(latest?.disks);
  const apps = overview?.apps;
  const presence = serverPresence(online, server.lastSeenAt, now);
  // The newest host sample may lack CPU (the first one after a restart has
  // no delta yet); the sparkline's newest minute stands in.
  const cpu = latest?.cpu ?? latestValue(rows, "cpu");
  const hasSpark = rows.filter((r) => typeof r.cpu === "number").length >= 2;

  const meta: string[] = [];
  if (latest) {
    meta.push(`Load ${latest.load1.toFixed(2)} / ${latest.load5.toFixed(2)} / ${latest.load15.toFixed(2)}`);
    if (latest.temp !== undefined) meta.push(formatTemp(latest.temp));
  } else {
    // Metrics off or not reported yet: app counts still show.
    meta.push("No data yet");
  }
  const quiet = [
    server.capabilities.server_ip,
    ...specsLine(server.capabilities),
    server.capabilities.version ? `Agent v${server.capabilities.version}` : "",
  ].filter(Boolean);

  return (
    <Card
      title={`${server.name}: ${online ? "online" : "offline"}`}
      onClick={onOpen}
      className="flex min-w-0 cursor-pointer flex-col gap-3 p-4 transition-colors hover:border-primary/40"
    >
      <div className="flex items-center justify-between gap-3">
        <div className="truncate text-base font-semibold tracking-tight">{server.name}</div>
        <span
          className="flex shrink-0 items-center gap-1.5 text-xs text-muted-foreground"
          title={presence.title}
          data-testid="presence"
        >
          <span aria-hidden className={cn("size-2 rounded-full", DOT[presence.tone])} />
          {presence.label}
        </span>
      </div>

      <div className="relative h-12" aria-label="CPU, last hour">
        {hasSpark ? (
          <Sparkline
            rows={rows}
            field="cpu"
            color={chartColor(0)}
            max={percentCeiling(rows, "cpu")}
            className="h-12"
          />
        ) : (
          // twillingate's quiet card: a hairline where the trend will be.
          <div className="absolute inset-x-0 bottom-1 border-b border-primary/30" />
        )}
      </div>

      <div className="space-y-1.5">
        <Meter label="CPU" percent={cpu} color={chartColor(0)} />
        <Meter
          label="Memory"
          percent={percentOf(latest?.mem_used, latest?.mem_total)}
          color={chartColor(1)}
        />
        <Meter
          label="Disk"
          percent={disk ? percentOf(disk.used, disk.total) : undefined}
          color={chartColor(2)}
          title={disk ? `Fullest filesystem: ${disk.mount}` : undefined}
        />
      </div>

      <div className="space-y-1">
        <div className="text-xs text-muted-foreground tabular-nums">
          {meta.join(" · ")}
          {apps ? (
            <span data-testid="app-counts">
              {" · "}
              <span className="text-foreground">{apps.running} running</span>
              {` · ${apps.stopped} stopped`}
              {apps.problematic > 0 ? (
                <span className="text-destructive">{` · ${apps.problematic} problematic`}</span>
              ) : null}
            </span>
          ) : null}
        </div>
        {quiet.length > 0 ? (
          <div className="text-xs text-muted-foreground/75">
            {quiet.join(" · ")}
          </div>
        ) : null}
      </div>
    </Card>
  );
}

// HealthCardSkeleton holds a server card's place while the list loads.
function HealthCardSkeleton() {
  return (
    <Card role="status" aria-label="Loading servers" className="flex flex-col gap-3 p-4">
      <div className="flex items-center justify-between">
        <div className="frost h-4 w-28 rounded" />
        <div className="frost h-3 w-14 rounded" />
      </div>
      <div className="frost h-12 rounded-md opacity-70" />
      <div className="space-y-2.5">
        {[0, 1, 2].map((i) => (
          <div key={i} className="frost h-1.5 rounded-full" />
        ))}
      </div>
      <div className="frost h-3 w-3/4 rounded" />
    </Card>
  );
}

const GRID = "grid gap-3 sm:grid-cols-2 xl:grid-cols-3";

// ServerCards renders the org's servers as health cards at the top of the
// dashboard: presence, the last hour of CPU, usage bars, load, temperature
// and app counts. Clicking one makes it the active server and opens its
// Monitoring page.
export function ServerCards() {
  const { servers, setActiveServerId, statusByServer, loading } = useServers();
  const { data } = useMetricsOverview();
  const navigate = useNavigate();
  const now = useNow();

  const byId = useMemo(() => {
    const m = new Map<string, ServerOverview>();
    for (const o of data?.servers ?? []) m.set(o.server_id, o);
    return m;
  }, [data]);

  if (loading && servers.length === 0) {
    return (
      <div className={GRID}>
        <HealthCardSkeleton />
      </div>
    );
  }
  if (servers.length === 0) {
    return null;
  }

  return (
    <div className={GRID}>
      {servers.map((s) => (
        <HealthCard
          key={s.id}
          server={s}
          overview={byId.get(s.id)}
          online={statusByServer[s.id] === "online"}
          now={now}
          onOpen={() => {
            setActiveServerId(s.id);
            navigate("/monitoring");
          }}
        />
      ))}
    </div>
  );
}
