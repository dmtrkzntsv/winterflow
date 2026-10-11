import { useMemo } from "react";
import { useNavigate } from "react-router-dom";
import { Server as ServerIcon } from "lucide-react";

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
  return parts.join(" • ");
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
  selected,
  onOpen,
}: {
  server: Server;
  overview: ServerOverview | undefined;
  online: boolean;
  selected: boolean;
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

  return (
    <Card
      title={`${server.name}: ${online ? "online" : "offline"}`}
      onClick={onOpen}
      className={cn(
        "w-[340px] cursor-pointer transition-all hover:bg-muted/50",
        selected && "border-primary ring-1 ring-primary",
      )}
    >
      <CardContent className="space-y-3 p-4">
        <div className="flex items-start justify-between gap-3">
          <div className="min-w-0">
            <div className="flex items-center gap-2">
              <span
                className={cn("size-2 shrink-0 rounded-full", online ? "bg-green-500" : "bg-gray-400")}
              />
              <span className="truncate font-medium">{server.name}</span>
            </div>
            <div className="mt-1 text-xs text-muted-foreground">
              {online
                ? `Online${server.capabilities.server_ip ? ` · ${server.capabilities.server_ip}` : ""}`
                : server.lastSeenAt
                  ? `Offline since ${formatSince(server.lastSeenAt)}`
                  : "Waiting for the agent to connect…"}
              {specs ? <div>{specs}</div> : null}
              {server.capabilities.version ? <div>Agent v{server.capabilities.version}</div> : null}
            </div>
          </div>
          <div
            className={cn(
              "flex size-10 shrink-0 items-center justify-center rounded-lg",
              online ? "bg-primary/10" : "bg-muted",
            )}
          >
            <ServerIcon className={cn("size-5", online ? "text-primary" : "text-muted-foreground")} />
          </div>
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
  const { servers, activeServerId, setActiveServerId, statusByServer, loading } = useServers();
  const { data } = useMetricsOverview();
  const navigate = useNavigate();

  const byId = useMemo(() => {
    const m = new Map<string, ServerOverview>();
    for (const o of data?.servers ?? []) m.set(o.server_id, o);
    return m;
  }, [data]);

  if (loading && servers.length === 0) {
    return <div className="bg-muted/50 h-[220px] w-[340px] rounded-xl" />;
  }
  if (servers.length === 0) {
    return null;
  }

  return (
    <div className="flex flex-wrap gap-4">
      {servers.map((s) => (
        <HealthCard
          key={s.id}
          server={s}
          overview={byId.get(s.id)}
          online={statusByServer[s.id] === "online"}
          selected={s.id === activeServerId}
          onOpen={() => {
            setActiveServerId(s.id);
            navigate("/monitoring");
          }}
        />
      ))}
    </div>
  );
}
