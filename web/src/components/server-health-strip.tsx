import type { ReactNode } from "react";
import { Link } from "react-router-dom";
import { ArrowRight } from "lucide-react";

import { StatusDot } from "@/components/status";
import { isStandalone } from "@/config";
import type { Server } from "@/context/servers-context-base";
import { useServers } from "@/context/use-servers";
import { useMetricsOverview } from "@/hooks/use-metrics";
import { useNow } from "@/hooks/use-now";
import { lastSeenText } from "@/lib/kpi";
import { formatPercent, fullestDisk, percentOf } from "@/lib/metrics";
import { monitoringPath } from "@/lib/routes";
import { serverStatus } from "@/lib/status";

// ServerHealthStrip is the Apps page's one line about this server: CPU,
// memory and the fullest disk now — or why there are no figures (offline,
// never connected, no data yet) — and the way to Monitoring. The server's
// IP, cores and agent version live in Settings → Server → General.
export function ServerHealthStrip({ server }: { server: Server }) {
  const { statusByServer } = useServers();
  const { data } = useMetricsOverview();
  const now = useNow();
  const entry = data?.servers?.find((s) => s.server_id === server.id);
  // Liveness is known once the servers context has it (or the overview does,
  // should the context's seed have failed); until then nothing is claimed.
  const liveness = isStandalone ? "online" : (statusByServer[server.id] ?? entry?.liveness);
  // The same mapping as the server switcher and Settings → General.
  const status = serverStatus({ liveness, lastSeenAt: server.lastSeenAt, standalone: isStandalone });
  const latest = entry?.latest?.server;

  let body: ReactNode;
  if (liveness === undefined) {
    body = <span className="text-muted-foreground">Checking this server…</span>;
  } else if (status.tone === "bad") {
    body = (
      <span className="inline-flex items-center gap-2">
        <StatusDot tone={status.tone} />
        <span className="font-medium">{status.label}</span>
        <span className="text-muted-foreground">· {lastSeenText(server.lastSeenAt, now)}</span>
      </span>
    );
  } else if (status.tone === "idle") {
    body = (
      <span className="inline-flex items-center gap-2 text-muted-foreground">
        <StatusDot tone="idle" />
        Waiting for this server to connect for the first time
      </span>
    );
  } else if (!latest) {
    body = <span className="text-muted-foreground">No data yet</span>;
  } else {
    const disk = fullestDisk(latest.disks);
    const figures: [string, number | undefined][] = [
      ["CPU", latest.cpu],
      ["Memory", percentOf(latest.mem_used, latest.mem_total)],
    ];
    if (disk) figures.push(["Disk", percentOf(disk.used, disk.total)]);
    body = figures.map(([label, value], i) => (
      <span key={label} className="whitespace-nowrap">
        {i > 0 ? (
          <span aria-hidden className="mr-3 text-muted-foreground/60">
            ·
          </span>
        ) : null}
        <span className="text-muted-foreground">{label}</span>{" "}
        <span className="font-medium tabular-nums">{formatPercent(value)}</span>
      </span>
    ));
  }

  return (
    <div
      role="region"
      aria-label="Server health"
      className="flex min-h-10 flex-wrap items-center gap-x-3 gap-y-1 rounded-xl border bg-card px-4 py-2 text-sm"
    >
      {body}
      <Link
        to={monitoringPath(server.id)}
        className="ml-auto inline-flex min-h-8 items-center gap-1 font-medium max-sm:min-h-10 text-muted-foreground transition-colors duration-150 hover:text-foreground"
      >
        Monitoring
        <ArrowRight className="size-4" aria-hidden />
      </Link>
    </div>
  );
}
