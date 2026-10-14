import { useCallback, useEffect, useState } from "react";

import { LogsView, type LogLine } from "@/components/logs-view";
import { useNotifications } from "@/context/use-notifications";
import { useServers } from "@/context/use-servers";
import { apiBaseUrl } from "@/config";
import { readApiError } from "@/lib/api";

const base = apiBaseUrl.endsWith("/") ? apiBaseUrl.slice(0, -1) : apiBaseUrl;

export function AppLogsTab({ appId }: { appId: string }) {
  const { activeServerId } = useServers();
  const { waitFor } = useNotifications();
  const [logs, setLogs] = useState<LogLine[]>([]);
  const [tail, setTail] = useState<number>(200);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const fetchLogs = useCallback(async () => {
    if (!activeServerId) return;
    setLoading(true);
    setError(null);
    try {
      const url = `${base}/api/v1/app/get-logs?server_id=${encodeURIComponent(
        activeServerId,
      )}&app_id=${encodeURIComponent(appId)}&tail=${tail}`;
      const res = await fetch(url, { credentials: "include" });
      if (!res.ok) throw await readApiError(res);
      const accepted = (await res.json()) as { data?: { request_id?: string } };
      const ref = accepted.data?.request_id;
      if (!ref) throw new Error("No request id");
      const result = await waitFor(ref);
      if (result.status && result.status !== 0) {
        throw new Error(result.error || "Failed to fetch logs");
      }
      const payload = result.payload as { logs?: LogLine[] } | undefined;
      setLogs(payload?.logs ?? []);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Failed to fetch logs");
    } finally {
      setLoading(false);
    }
  }, [appId, activeServerId, waitFor, tail]);

  useEffect(() => {
    void fetchLogs();
  }, [fetchLogs]);

  return (
    <LogsView
      lines={logs}
      loading={loading}
      error={error}
      tail={tail}
      onTailChange={setTail}
      onRefresh={() => void fetchLogs()}
    />
  );
}
