import { useCallback, useEffect, useState } from "react";

import {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet";
import { Button } from "@/components/ui/button";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Spinner } from "@/components/ui/spinner";
import { useNotifications } from "@/context/use-notifications";
import { useServers } from "@/context/use-servers";
import { apiBaseUrl } from "@/config";
import type { App } from "@/context/apps-context-base";

const base = apiBaseUrl.endsWith("/") ? apiBaseUrl.slice(0, -1) : apiBaseUrl;

type LogEntry = {
  timestamp: number;
  level: number;
  message: string;
  container?: string;
};

// Tailwind classes per log level (matches command.LogLevel ordering).
const LEVEL_CLASS: Record<number, string> = {
  4: "text-yellow-500", // warn
  5: "text-red-500", // error
  6: "text-red-600 font-semibold", // fatal
};

export function AppLogsSheet({
  app,
  open,
  onOpenChange,
}: {
  app: App | null;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const { activeServerId } = useServers();
  const { waitFor } = useNotifications();
  const [logs, setLogs] = useState<LogEntry[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const fetchLogs = useCallback(async () => {
    if (!app || !activeServerId) return;
    setLoading(true);
    setError(null);
    try {
      const url = `${base}/api/v1/app/get-logs?server_id=${encodeURIComponent(
        activeServerId,
      )}&app_id=${encodeURIComponent(app.id)}&tail=200`;
      const res = await fetch(url, { credentials: "include" });
      if (!res.ok) throw new Error(`Request failed: ${res.status}`);
      const accepted = (await res.json()) as { data?: { request_id?: string } };
      const ref = accepted.data?.request_id;
      if (!ref) throw new Error("No request id");
      const result = await waitFor(ref);
      if (result.status && result.status !== 0) {
        throw new Error(result.error || "Failed to fetch logs");
      }
      const payload = result.payload as { logs?: LogEntry[] } | undefined;
      setLogs(payload?.logs ?? []);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Failed to fetch logs");
    } finally {
      setLoading(false);
    }
  }, [app, activeServerId, waitFor]);

  useEffect(() => {
    if (open && app) void fetchLogs();
  }, [open, app, fetchLogs]);

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent className="flex w-full flex-col gap-0 sm:max-w-2xl">
        <SheetHeader className="flex-row items-center justify-between space-y-0">
          <SheetTitle>Logs — {app?.name}</SheetTitle>
          <Button
            size="sm"
            variant="outline"
            onClick={() => void fetchLogs()}
            disabled={loading}
          >
            Refresh
          </Button>
        </SheetHeader>
        <div className="min-h-0 flex-1 px-4 pb-4">
          {loading ? (
            <div className="flex h-full items-center justify-center">
              <Spinner />
            </div>
          ) : error ? (
            <p className="text-sm text-destructive">{error}</p>
          ) : logs.length === 0 ? (
            <p className="text-sm text-muted-foreground">No logs.</p>
          ) : (
            <ScrollArea className="h-full rounded-md border bg-muted/30">
              <div className="p-3 font-mono text-xs">
                {logs.map((l, i) => (
                  <div key={i} className="whitespace-pre-wrap break-all">
                    {l.container ? (
                      <span className="text-muted-foreground">
                        {l.container}{" "}
                      </span>
                    ) : null}
                    <span className={LEVEL_CLASS[l.level] ?? ""}>
                      {l.message}
                    </span>
                  </div>
                ))}
              </div>
            </ScrollArea>
          )}
        </div>
      </SheetContent>
    </Sheet>
  );
}
