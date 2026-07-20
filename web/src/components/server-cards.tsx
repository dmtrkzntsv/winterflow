import { Card, CardContent } from "@/components/ui/card";
import { Server as ServerIcon } from "lucide-react";
import { cn, formatBytes } from "@/lib/utils";
import { useServers } from "@/context/use-servers";

// ServerCards renders the org's servers as a selectable row at the top of the
// dashboard (v1's AgentList equivalent): status dot, IP, hardware specs and
// agent version from capabilities, last-seen when the server isn't online.
// Clicking a card makes it the active server, which scopes the apps list.
export function ServerCards() {
  const { servers, activeServerId, setActiveServerId, statusByServer, loading } =
    useServers();

  if (loading && servers.length === 0) {
    return <div className="bg-muted/50 h-[120px] w-[340px] rounded-xl" />;
  }
  if (servers.length === 0) {
    return null;
  }

  const formatLastSeen = (iso: string | null) => {
    if (!iso) return "never";
    const d = new Date(iso);
    return Number.isNaN(d.getTime()) ? "never" : d.toLocaleString();
  };

  const specsLine = (caps: Record<string, string>) => {
    const parts: string[] = [];
    if (caps.system_cpu_cores) parts.push(`${caps.system_cpu_cores} cores`);
    const mem = formatBytes(caps.system_memory_total);
    if (mem) parts.push(mem);
    const disk = formatBytes(caps.system_disk_total);
    if (disk) parts.push(`${disk} disk`);
    return parts.join(" • ");
  };

  return (
    <div className="flex flex-wrap gap-4">
      {servers.map((s) => {
        const selected = s.id === activeServerId;
        const online = statusByServer[s.id] === "online";
        const specs = specsLine(s.capabilities);
        return (
          <Card
            key={s.id}
            title={`${s.name}: ${online ? "online" : "unknown"}`}
            onClick={() => setActiveServerId(s.id)}
            className={cn(
              "w-[340px] cursor-pointer transition-all hover:bg-muted/50",
              selected && "border-primary ring-1 ring-primary",
            )}
          >
            <CardContent className="flex items-start justify-between gap-3 p-4">
              <div className="min-w-0">
                <div className="flex items-center gap-2">
                  <span
                    className={cn(
                      "size-2 shrink-0 rounded-full",
                      online ? "bg-green-500" : "bg-gray-400",
                    )}
                  />
                  <span className="truncate font-medium">{s.name}</span>
                </div>
                <div className="mt-1.5 space-y-0.5 text-xs text-muted-foreground">
                  {s.capabilities.server_ip ? (
                    <div className="font-mono">{s.capabilities.server_ip}</div>
                  ) : null}
                  {specs ? <div>{specs}</div> : null}
                  {s.capabilities.version ? (
                    <div>Agent v{s.capabilities.version}</div>
                  ) : null}
                  {!online ? (
                    <div>Last seen: {formatLastSeen(s.lastSeenAt)}</div>
                  ) : null}
                </div>
              </div>
              <div
                className={cn(
                  "flex size-12 shrink-0 items-center justify-center rounded-lg",
                  online ? "bg-primary/10" : "bg-muted",
                )}
              >
                <ServerIcon
                  className={cn(
                    "size-6",
                    online ? "text-primary" : "text-muted-foreground",
                  )}
                />
              </div>
            </CardContent>
          </Card>
        );
      })}
    </div>
  );
}
