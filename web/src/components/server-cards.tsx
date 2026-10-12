import { useMemo } from "react";
import { useNavigate } from "react-router-dom";

import { Card, CardContent } from "@/components/ui/card";
import { Progress } from "@/components/ui/progress";
import { Sparkline } from "@/components/sparkline";
import { useServers } from "@/context/use-servers";
import type { Server } from "@/context/servers-context-base";
import { useMetricsOverview } from "@/hooks/use-metrics";
import {
  chartColor,
  formatPercent,
  formatTemp,
  fullestDisk,
  percentOf,
  toChartRows,
  type ChartRow,
  type ServerOverview,
} from "@/lib/metrics";
import { cn, formatBytes } from "@/lib/utils";

const SPARK_INTERVAL = 60; // the overview sparkline is tier 1: one point a minute

function formatSince(iso: string | null): string {
  if (!iso) return "never";
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? "never" : d.toLocaleString();
}

function specsLine(caps: Record<string, string>): string {
  const parts: string[] = [];
  if (caps.system_cpu_cores) parts.push(`${caps.system_cpu_cores} cores`);
  const mem = formatBytes(caps.system_memory_total);
  if (mem) parts.push(mem);
  const disk = formatBytes(caps.system_disk_total);
  if (disk) parts.push(`${disk} disk`);
  return parts.join(" · ");
}

// Meter is one labelled usage bar with its 1-hour sparkline.
function Meter({
  label,
  percent,
  detail,
  rows,
  field,
  color,
}: {
  label: string;
  percent: number | undefined;
  detail?: string;
  rows: ChartRow[];
  field: string;
  color: string;
}) {
  return (
    <div className="space-y-1" data-testid={`meter-${label}`}>
      <div className="flex items-baseline justify-between text-xs">
        <span className="text-muted-foreground">{label}</span>
        <span className="font-medium tabular-nums">
          {formatPercent(percent)}
          {detail ? <span className="ml-1 font-normal text-muted-foreground">{detail}</span> : null}
        </span>
      </div>
      <Progress value={percent ?? 0} className="h-1.5" />
      <Sparkline rows={rows} field={field} color={color} max={100} />
    </div>
  );
}

// HealthCard is one server's dashboard widget.
function HealthCard({
  server,
  overview,
  online,
  onOpen,
}: {
  server: Server;
  overview: ServerOverview | undefined;
  online: boolean;
  onOpen: () => void;
}) {
  const latest = overview?.latest?.server;
  const rows = useMemo(() => {
    // Every sparkline is a percentage; memory's is derived from used/total.
    const pts = (overview?.sparkline ?? []).map((p) => {
      const mem = percentOf(p.mem_used, p.mem_total);
      return mem === undefined ? p : { ...p, mem_pct: mem };
    });
    return toChartRows(pts, SPARK_INTERVAL, ["cpu", "mem_pct", "disk_pct"], "avg");
  }, [overview?.sparkline]);
  const disk = fullestDisk(latest?.disks);
  const apps = overview?.apps;
  const specs = specsLine(server.capabilities);

  const status = online
    ? "Online"
    : server.lastSeenAt
      ? "Offline"
      : "Waiting";
  const meta = [
    online
      ? server.capabilities.server_ip
      : server.lastSeenAt
        ? `Offline since ${formatSince(server.lastSeenAt)}`
        : "Waiting for the agent to connect…",
    specs,
    server.capabilities.version ? `Agent v${server.capabilities.version}` : "",
  ].filter(Boolean);

  // State is a small dot plus a word, not a coloured outline (twillingate's
  // project card); the whole card opens the server's Monitoring page.
  return (
    <Card
      title={`${server.name}: ${online ? "online" : "offline"}`}
      onClick={onOpen}
      className="w-full cursor-pointer transition-colors hover:border-primary/40 sm:w-[340px]"
    >
      <CardContent className="space-y-3 p-4">
        <div className="flex items-start justify-between gap-3">
          <div className="min-w-0">
            <div className="truncate text-base font-semibold tracking-tight">{server.name}</div>
            {meta.length > 0 ? (
              <div className="mt-0.5 text-xs text-muted-foreground">{meta.join(" · ")}</div>
            ) : null}
          </div>
          <span className="mt-1 flex shrink-0 items-center gap-1.5 text-xs text-muted-foreground">
            <span
              aria-hidden
              className={cn("size-2 rounded-full", online ? "bg-emerald-500" : "bg-muted-foreground/40")}
            />
            {status}
          </span>
        </div>

        <div className="space-y-2">
          <Meter label="CPU" percent={latest?.cpu} rows={rows} field="cpu" color={chartColor(0)} />
          <Meter
            label="Memory"
            percent={percentOf(latest?.mem_used, latest?.mem_total)}
            rows={rows}
            field="mem_pct"
            color={chartColor(1)}
          />
          <Meter
            label="Disk"
            percent={disk ? percentOf(disk.used, disk.total) : undefined}
            detail={disk?.mount}
            rows={rows}
            field="disk_pct"
            color={chartColor(2)}
          />
          {latest ? (
            <div className="flex flex-wrap gap-x-3 text-xs text-muted-foreground">
              <span>
                Load {latest.load1.toFixed(2)} / {latest.load5.toFixed(2)} / {latest.load15.toFixed(2)}
              </span>
              {latest.temp !== undefined ? <span>{formatTemp(latest.temp)}</span> : null}
            </div>
          ) : (
            <div className="text-xs text-muted-foreground">No data yet</div>
          )}
        </div>

        {apps ? (
          <div className="flex gap-3 text-xs" data-testid="app-counts">
            <span>{apps.running} running</span>
            <span className="text-muted-foreground">{apps.stopped} stopped</span>
            {apps.problematic > 0 ? (
              <span className="text-destructive">{apps.problematic} problematic</span>
            ) : null}
          </div>
        ) : null}
      </CardContent>
    </Card>
  );
}

// ServerCards renders the org's servers as health widgets at the top of the
// dashboard: liveness, CPU/memory/disk meters with 1-hour sparklines, load,
// temperature and app counts. Clicking one makes it the active server and
// opens its Monitoring page.
export function ServerCards() {
  const { servers, setActiveServerId, statusByServer, loading } = useServers();
  const { data } = useMetricsOverview();
  const navigate = useNavigate();

  const byId = useMemo(() => {
    const m = new Map<string, ServerOverview>();
    for (const o of data?.servers ?? []) m.set(o.server_id, o);
    return m;
  }, [data]);

  if (loading && servers.length === 0) {
    return <div className="frost h-[220px] w-full rounded-xl sm:w-[340px]" />;
  }
  if (servers.length === 0) {
    return null;
  }

  return (
    <div className="flex flex-wrap gap-3">
      {servers.map((s) => (
        <HealthCard
          key={s.id}
          server={s}
          overview={byId.get(s.id)}
          online={statusByServer[s.id] === "online"}
          onOpen={() => {
            setActiveServerId(s.id);
            navigate("/monitoring");
          }}
        />
      ))}
    </div>
  );
}
