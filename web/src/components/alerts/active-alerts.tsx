import { Link } from "react-router-dom";
import { BellRing, Flame } from "lucide-react";

import { Card } from "@/components/ui/card";
import { useActiveAlerts } from "@/hooks/use-alerts";
import { useNow } from "@/hooks/use-now";
import { activeText, alertTargetPath, sinceText } from "@/lib/alerts";
import { cn } from "@/lib/utils";

// FiringBadge is the small red count of firing alerts on a server card or
// an app header. Nothing when there are none.
export function FiringBadge({ count, className }: { count: number; className?: string }) {
  if (count <= 0) return null;
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1 rounded-full bg-destructive/10 px-1.5 py-0.5 text-xs font-medium text-destructive",
        className,
      )}
      title={`${count} firing alert${count === 1 ? "" : "s"}`}
      data-testid="firing-badge"
    >
      <BellRing className="size-3" aria-hidden />
      {count}
    </span>
  );
}

// AppAlertsBadge is an app header's firing count, linking to its Metrics
// tab where the alerts card is.
export function AppAlertsBadge({ serverId, appId }: { serverId: string; appId: string }) {
  const { data } = useActiveAlerts(serverId, appId);
  if (data.length === 0) return null;
  return (
    <Link to="?tab=metrics" aria-label="Firing alerts">
      <FiringBadge count={data.length} />
    </Link>
  );
}

// MAX_STRIP is how many firing alerts the dashboard lists before "View all".
const MAX_STRIP = 5;

// ActiveAlertsStrip lists the org's firing alerts above the dashboard's
// server health. It renders nothing while all is quiet.
export function ActiveAlertsStrip() {
  const { data } = useActiveAlerts();
  const now = useNow();
  if (data.length === 0) return null;
  return (
    <Card className="gap-2 border-destructive/30 p-3.5" role="region" aria-label="Active alerts">
      <div className="flex items-center justify-between gap-3">
        <h2 className="flex items-center gap-2 text-sm font-medium text-destructive">
          <Flame className="size-4" aria-hidden />
          {data.length} active alert{data.length === 1 ? "" : "s"}
        </h2>
        <Link to="/org/alerts" className="text-xs text-muted-foreground hover:text-foreground hover:underline">
          View history
        </Link>
      </div>
      <ul className="grid gap-1 text-sm">
        {data.slice(0, MAX_STRIP).map((a) => (
          <li key={`${a.rule_id}-${a.server_id}-${a.app_id}`} className="flex min-w-0 items-center gap-2">
            <Link to={alertTargetPath(a.server_id, a.app_id)} className="truncate font-medium hover:underline">
              {a.app_id ? `${a.server_name} / ${a.app_name || a.app_id}` : a.server_name}
            </Link>
            <span className="truncate text-muted-foreground">{activeText(a)}</span>
            <span className="ml-auto shrink-0 text-xs text-muted-foreground tabular-nums">
              {sinceText(a.since, now)}
            </span>
          </li>
        ))}
      </ul>
      {data.length > MAX_STRIP ? (
        <p className="text-xs text-muted-foreground">and {data.length - MAX_STRIP} more</p>
      ) : null}
    </Card>
  );
}
