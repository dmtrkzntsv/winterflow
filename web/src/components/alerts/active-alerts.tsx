import { Link } from "react-router-dom";
import { ArrowRight, TriangleAlert } from "lucide-react";

import { useActiveAlerts } from "@/hooks/use-alerts";
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
