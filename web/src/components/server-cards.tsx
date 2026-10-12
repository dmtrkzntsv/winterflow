import { useMemo } from "react";
import { useNavigate } from "react-router-dom";

import { Card } from "@/components/ui/card";
import { useServers } from "@/context/use-servers";
import type { Server } from "@/context/servers-context-base";
import { FiringBadge } from "@/components/alerts/active-alerts";
import { useMetricsOverview } from "@/hooks/use-metrics";
import { useNow } from "@/hooks/use-now";
import { TILE_GRID } from "@/lib/grid";
import { latestValue, serverPresence, type Presence } from "@/lib/kpi";
import {
  chartColor,
  formatPercent,
  formatTemp,
  fullestDisk,
  percentOf,
  toChartRows,
  type ServerOverview,
} from "@/lib/metrics";
import { serverPath } from "@/lib/server-paths";
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
      className="grid grid-cols-[3.5rem_1fr_2.5rem] items-center gap-2 text-xs"
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

// HealthCard is one server's compact health card: name and a presence dot,
// slim CPU/Memory/Disk bars, then a meta line and a quieter specs line. The
// whole card opens the server's Monitoring page.
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

  const meta: string[] = [];
  if (latest) {
    meta.push(`Load ${latest.load1.toFixed(2)}`);
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
  const specs = quiet.join(" · ");

  return (
    // The whole tile opens the server's Monitoring page, by mouse or keyboard.
    <Card
      role="link"
      tabIndex={0}
      aria-label={`${server.name}: ${online ? "online" : "offline"}, open monitoring`}
      title={`${server.name}: ${online ? "online" : "offline"}`}
      onClick={onOpen}
      onKeyDown={(e) => {
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          onOpen();
        }
      }}
      className="flex min-w-0 cursor-pointer flex-col gap-2 p-3.5 transition-colors outline-none hover:border-primary/40 focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background"
    >
      <div className="flex items-center justify-between gap-3">
        <div className="flex min-w-0 items-center gap-2">
          <div className="truncate text-base font-semibold tracking-tight">{server.name}</div>
          <FiringBadge count={overview?.alerts_firing ?? 0} />
        </div>
        <span
          className="flex shrink-0 items-center gap-1.5 text-xs text-muted-foreground"
          title={presence.title}
          data-testid="presence"
        >
          <span aria-hidden className={cn("size-2 rounded-full", DOT[presence.tone])} />
          {presence.label}
        </span>
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

      <div
        className="truncate text-xs text-muted-foreground tabular-nums"
        title={latest ? `Load ${latest.load1.toFixed(2)} / ${latest.load5.toFixed(2)} / ${latest.load15.toFixed(2)}` : undefined}
      >
        {meta.join(" · ")}
        {apps ? (
          <span data-testid="app-counts">
            {" · "}
            <span className="text-foreground">{apps.running} running</span>
            {apps.problematic > 0 ? (
              <span className="text-destructive">{` · ${apps.problematic} problematic`}</span>
            ) : null}
          </span>
        ) : null}
      </div>
      {/* Reserve the line so tiles stay equal height with or without specs. */}
      <div className="h-4 truncate text-xs text-muted-foreground" title={specs || undefined}>
        {specs}
      </div>
    </Card>
  );
}

// HealthCardSkeleton holds a server card's place while the list loads.
function HealthCardSkeleton() {
  return (
    <Card
      role="status"
      aria-label="Loading servers"
      data-testid="server-card-skeleton"
      className="flex min-w-0 flex-col gap-2 p-3.5"
    >
      <div data-testid="skeleton-head" className="flex items-center justify-between gap-3">
        <div className="frost h-6 w-28 rounded" />
        <div className="frost h-4 w-14 rounded" />
      </div>
      <div data-testid="skeleton-body" className="space-y-1.5">
        {[0, 1, 2].map((i) => (
          <div key={i} className="frost h-4 rounded" />
        ))}
      </div>
      <div className="frost h-4 w-3/4 rounded" />
      <div className="h-4" />
    </Card>
  );
}

// ServerCards renders the org's servers as a compact health strip above the
// apps on the dashboard: presence, usage bars, load, temperature and app
// counts. Clicking one opens that server's Monitoring page (its
// /server/:serverId/monitoring URL).
export function ServerCards() {
  const { servers, statusByServer, loading } = useServers();
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
      <div className={TILE_GRID}>
        <HealthCardSkeleton />
      </div>
    );
  }
  if (servers.length === 0) {
    return null;
  }

  return (
    <div className={TILE_GRID}>
      {servers.map((s) => (
        <HealthCard
          key={s.id}
          server={s}
          overview={byId.get(s.id)}
          online={statusByServer[s.id] === "online"}
          now={now}
          onOpen={() => navigate(serverPath(s.id, "/monitoring"))}
        />
      ))}
    </div>
  );
}
