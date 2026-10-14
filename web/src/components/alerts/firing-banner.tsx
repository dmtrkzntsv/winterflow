import { Link } from "react-router-dom";
import { ArrowRight, TriangleAlert } from "lucide-react";

import { StatusDot } from "@/components/status";
import { useActiveAlerts } from "@/hooks/use-alerts";
import { useNow } from "@/hooks/use-now";
import { alertTargetPath, sinceText } from "@/lib/alerts";
import { activeTitle } from "@/lib/alert-text";
import { INBOX_PATH } from "@/lib/routes";

// FiringBanner is the top banner of a server's Monitoring page (or an
// app's page): what is firing there now, in plain words, with how long.
// On a server it also lists its apps' alerts and links each one to the app.
// It lists at most MAX_LINES alerts, then "and N more" leading to the
// Inbox, so a storm of alerts cannot push the charts off the screen. It
// renders nothing while all is quiet.
const MAX_LINES = 5;

export function FiringBanner({ serverId, appId }: { serverId: string; appId?: string }) {
  const { data } = useActiveAlerts(serverId, appId || null);
  const now = useNow();
  const firing = appId ? data.filter((a) => a.app_id === appId) : data;
  if (firing.length === 0) return null;
  const shown = firing.slice(0, MAX_LINES);
  const more = firing.length - shown.length;
  return (
    <div
      role="region"
      aria-label="Firing alerts"
      className="rounded-xl border border-status-bad/30 bg-status-bad/5 px-4 py-3"
    >
      <div className="flex items-center justify-between gap-3">
        <h2 className="flex items-center gap-2 text-sm font-semibold text-foreground">
          <TriangleAlert className="size-4 text-status-bad" aria-hidden />
          {firing.length} alert{firing.length === 1 ? "" : "s"} firing
        </h2>
        <Link
          to={INBOX_PATH}
          className="inline-flex min-h-8 items-center gap-1 text-xs max-sm:min-h-10 text-muted-foreground transition-colors duration-150 hover:text-foreground"
        >
          Inbox
          <ArrowRight className="size-3.5" aria-hidden />
        </Link>
      </div>
      <ul className="mt-1 grid gap-1 text-sm">
        {shown.map((a) => {
          const title = activeTitle(a);
          // An app's alert on its server's page leads to the app; an alert
          // about the page itself needs no link.
          const linked = Boolean(a.app_id) && !appId;
          return (
            <li key={`${a.rule_id}-${a.app_id}`} className="flex min-w-0 items-center gap-2">
              <StatusDot tone="bad" />
              {linked ? (
                <Link to={alertTargetPath(a.server_id, a.app_id)} className="truncate font-medium hover:underline">
                  {title}
                </Link>
              ) : (
                <span className="truncate font-medium">{title}</span>
              )}
              <span className="ml-auto shrink-0 text-xs text-muted-foreground tabular-nums">
                for {sinceText(a.since, now)}
              </span>
            </li>
          );
        })}
      </ul>
      {more > 0 ? (
        <Link
          to={INBOX_PATH}
          className="mt-1 inline-flex min-h-8 items-center text-xs text-muted-foreground transition-colors duration-150 hover:text-foreground hover:underline max-sm:min-h-10"
        >
          and {more} more
        </Link>
      ) : null}
    </div>
  );
}
