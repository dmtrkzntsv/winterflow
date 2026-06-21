import { Card, CardContent } from "@/components/ui/card";
import { Server as ServerIcon } from "lucide-react";
import { cn } from "@/lib/utils";
import { useServers } from "@/context/use-servers";

// ServerCards renders the org's servers as a selectable row at the top of the
// dashboard (v1's AgentList equivalent). Clicking a card makes it the active
// server, which scopes the apps list below.
export function ServerCards() {
  const { servers, activeServerId, setActiveServerId, loading } = useServers();

  if (loading && servers.length === 0) {
    return <div className="bg-muted/50 h-[120px] w-[320px] rounded-xl" />;
  }
  if (servers.length === 0) {
    return null;
  }

  const formatLastSeen = (iso: string | null) => {
    if (!iso) return "—";
    const d = new Date(iso);
    return Number.isNaN(d.getTime()) ? "—" : d.toLocaleString();
  };

  return (
    <div className="flex flex-wrap gap-4">
      {servers.map((s) => {
        const selected = s.id === activeServerId;
        return (
          <Card
            key={s.id}
            onClick={() => setActiveServerId(s.id)}
            className={cn(
              "w-[320px] cursor-pointer transition-colors hover:bg-muted/50",
              selected && "border-primary ring-1 ring-primary",
            )}
          >
            <CardContent className="flex items-start justify-between gap-3 p-4">
              <div className="min-w-0">
                <div className="truncate font-medium">{s.name}</div>
                <div className="mt-1 space-y-0.5 text-xs text-muted-foreground">
                  <div>Last seen: {formatLastSeen(s.lastSeenAt)}</div>
                </div>
              </div>
              <div className="flex size-12 shrink-0 items-center justify-center rounded-lg bg-muted">
                <ServerIcon className="size-6 text-muted-foreground" />
              </div>
            </CardContent>
          </Card>
        );
      })}
    </div>
  );
}
