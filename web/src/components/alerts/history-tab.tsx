import { useCallback, useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { CircleCheck, Flame } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { useServers } from "@/context/use-servers";
import { alertsGet } from "@/hooks/use-alerts";
import type { AlertEvent } from "@/lib/alerts";
import { serverPath } from "@/lib/server-paths";

type Page = { events: AlertEvent[] | null; next_cursor: string };

const ALL = "all";

// targetPath links an event to its server's Monitoring page or its app's
// Metrics tab.
function targetPath(ev: AlertEvent): string {
  return ev.app_id
    ? serverPath(ev.server_id, `/app/${encodeURIComponent(ev.app_id)}?tab=metrics`)
    : serverPath(ev.server_id, "/monitoring");
}

// HistoryTab is the org's alert history, newest first, filterable by kind
// and server, with each event's delivery outcome per channel.
export function HistoryTab() {
  const { servers } = useServers();
  const [kind, setKind] = useState(ALL);
  const [server, setServer] = useState(ALL);
  const [events, setEvents] = useState<AlertEvent[]>([]);
  const [cursor, setCursor] = useState("");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(
    async (after: string) => {
      setLoading(true);
      const params: Record<string, string> = {};
      if (kind !== ALL) params.kind = kind;
      if (server !== ALL) params.server_id = server;
      if (after) params.cursor = after;
      try {
        const page = await alertsGet<Page>("get-events", params);
        setEvents((prev) => (after ? [...prev, ...(page.events ?? [])] : (page.events ?? [])));
        setCursor(page.next_cursor);
        setError(null);
      } catch (e) {
        setError(e instanceof Error ? e.message : "Failed to load history");
      } finally {
        setLoading(false);
      }
    },
    [kind, server],
  );

  useEffect(() => {
    void load("");
  }, [load]);

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap gap-2">
        <Select value={kind} onValueChange={setKind}>
          <SelectTrigger className="w-36" aria-label="Kind">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value={ALL}>All events</SelectItem>
            <SelectItem value="fired">Fired</SelectItem>
            <SelectItem value="resolved">Resolved</SelectItem>
          </SelectContent>
        </Select>
        {servers.length > 1 ? (
          <Select value={server} onValueChange={setServer}>
            <SelectTrigger className="w-44" aria-label="Server">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={ALL}>All servers</SelectItem>
              {servers.map((s) => (
                <SelectItem key={s.id} value={s.id}>
                  {s.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        ) : null}
      </div>
      {error ? <p className="text-sm text-destructive">{error}</p> : null}
      {!loading && events.length === 0 ? (
        <p className="text-sm text-muted-foreground">No alerts yet. Fired and resolved alerts show up here.</p>
      ) : null}
      <div className="grid gap-2">
        {events.map((ev) => (
          <Card key={ev.id} className="flex items-start gap-3 p-3.5" data-testid="event-row">
            {ev.kind === "fired" ? (
              <Flame className="mt-0.5 size-4 shrink-0 text-destructive" aria-label="Fired" />
            ) : (
              <CircleCheck className="mt-0.5 size-4 shrink-0 text-emerald-600 dark:text-emerald-400" aria-label="Resolved" />
            )}
            <div className="min-w-0 flex-1 space-y-1">
              <div className="truncate font-medium">{ev.title}</div>
              <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground">
                <time dateTime={ev.created_at}>{new Date(ev.created_at).toLocaleString()}</time>
                <Link to={targetPath(ev)} className="hover:text-foreground hover:underline">
                  {ev.app_name ? `${ev.server_name} / ${ev.app_name}` : ev.server_name}
                </Link>
                {(ev.deliveries ?? []).length === 0 ? <span>Not sent</span> : null}
                {(ev.deliveries ?? []).map((d) => (
                  <Badge
                    key={d.channel_id}
                    variant={d.ok ? "secondary" : "destructive"}
                    title={d.ok ? "Delivered" : d.error}
                  >
                    {d.ok ? "✓" : "✗"} {d.channel_name}
                  </Badge>
                ))}
              </div>
            </div>
          </Card>
        ))}
      </div>
      {cursor ? (
        <Button variant="outline" size="sm" onClick={() => void load(cursor)} disabled={loading}>
          Load more
        </Button>
      ) : null}
    </div>
  );
}
