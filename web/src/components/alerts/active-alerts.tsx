import { Link } from "react-router-dom";
import { ArrowRight, TriangleAlert } from "lucide-react";

import { useActiveAlerts } from "@/hooks/use-alerts";
import { useNow } from "@/hooks/use-now";
import { activeText, sinceText } from "@/lib/alerts";
import { INBOX_PATH } from "@/lib/routes";

// FiringAlertsLine is the Apps page's one line about the alerts firing on
// this server and its apps, leading to the Inbox. Nothing while all is
// quiet.
export function FiringAlertsLine({ serverId }: { serverId: string }) {
  const { data } = useActiveAlerts(serverId);
  if (data.length === 0) return null;
  return (
    <Link
      to={INBOX_PATH}
      className="flex min-h-10 items-center gap-2 rounded-xl border border-status-bad/30 bg-status-bad/10 px-4 py-2 text-sm font-medium transition-colors duration-150 hover:bg-status-bad/15"
    >
      <TriangleAlert className="size-4 shrink-0 text-status-bad" aria-hidden />
      {data.length} alert{data.length === 1 ? "" : "s"} firing
      <span className="ml-auto inline-flex items-center gap-1 text-xs text-muted-foreground">
        Inbox <ArrowRight className="size-3.5" aria-hidden />
      </span>
    </Link>
  );
}

// AppAlertsBanner is the app page's banner while alerts fire on the app:
// what fires and for how long, and the way to its metrics. Nothing while
// all is quiet.
export function AppAlertsBanner({ serverId, appId }: { serverId: string; appId: string }) {
  const { data } = useActiveAlerts(serverId, appId);
  const now = useNow();
  const mine = data.filter((a) => a.app_id === appId);
  if (mine.length === 0) return null;
  return (
    <div
      role="group"
      aria-label="Firing alerts"
      className="flex flex-wrap items-start gap-x-3 gap-y-1 rounded-xl border border-status-bad/30 bg-status-bad/10 px-4 py-2.5 text-sm"
    >
      <TriangleAlert className="mt-0.5 size-4 shrink-0 text-status-bad" aria-hidden />
      {/* The ages tick every minute: only what fires is announced, and only
          when that changes. */}
      <span role="status" className="sr-only">
        {mine.map((a) => activeText(a)).join(", ")}
      </span>
      <ul className="min-w-0 flex-1 space-y-0.5">
        {mine.map((a) => (
          <li key={`${a.rule_id}-${a.app_id}`}>
            <span className="font-medium">{activeText(a)}</span>
            <span className="text-muted-foreground"> · {sinceText(a.since, now)}</span>
          </li>
        ))}
      </ul>
      <Link
        to="?tab=metrics"
        className="inline-flex min-h-8 items-center text-sm font-medium underline-offset-4 hover:underline max-sm:min-h-10"
      >
        View metrics
      </Link>
    </div>
  );
}
