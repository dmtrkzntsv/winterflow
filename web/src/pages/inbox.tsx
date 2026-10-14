import { useState } from "react";
import { Link } from "react-router-dom";
import { ArrowRight, Inbox as InboxIcon } from "lucide-react";

import { EventRow, FiringRow } from "@/components/alerts/inbox-rows";
import { EmptyState } from "@/components/empty-state";
import { PageHeader } from "@/components/page-header";
import { RowList, Section } from "@/components/section";
import { Button } from "@/components/ui/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { useServers } from "@/context/use-servers";
import { useAlertChannels } from "@/hooks/use-alert-channels";
import { useAlertEvents, type EventKindFilter } from "@/hooks/use-alert-events";
import { useActiveAlerts } from "@/hooks/use-alerts";
import { useInboxUnread } from "@/hooks/use-inbox-unread";
import { useNow } from "@/hooks/use-now";
import { groupByDay } from "@/lib/alert-text";
import { settingsPath } from "@/lib/routes";

const ALL_SERVERS = "all";
const SHOW: { value: EventKindFilter; label: string }[] = [
  { value: "all", label: "All" },
  { value: "fired", label: "Firing" },
  { value: "resolved", label: "Resolved" },
];
const QUIET = "All quiet. You'll see alerts here when something needs attention.";
const FILTER_ITEM =
  "h-10 min-w-0 rounded-md px-3 sm:h-[30px] text-xs font-medium text-muted-foreground hover:bg-transparent hover:text-foreground data-[state=on]:bg-background dark:data-[state=on]:bg-foreground/15 data-[state=on]:text-foreground data-[state=on]:shadow-sm";

// InboxPage is /inbox, org-wide (spec §5): what is firing now pinned on top,
// then the alert history grouped by day, newest first, with unread dots
// (a firing newer than the user's last "Mark all as read", kept in this
// browser; the unread state is the shell's, shared with the sidebar badge).
// Filters: All / Firing / Resolved, and a server filter when there are
// several servers. Without a notification channel one line under the header
// says alerts only show here (rows don't repeat it).
export default function InboxPage() {
  const { servers } = useServers();
  const { count, isUnread, markAllRead } = useInboxUnread();
  const [show, setShow] = useState<EventKindFilter>("all");
  const [picked, setPicked] = useState(ALL_SERVERS);
  // The server filter exists with two servers or more; a picked server that
  // went away (or the filter disappearing) reads as all servers.
  const serverFilter = servers.length > 1;
  const serverId = serverFilter && servers.some((s) => s.id === picked) ? picked : null;
  const { events, hasMore, loading, error, loadMore } = useAlertEvents(show, serverId);
  const { data: active } = useActiveAlerts(serverId);
  const now = useNow();
  const firing = show === "resolved" ? [] : active;
  const groups = groupByDay(events, now);
  const quiet = !loading && !error && events.length === 0 && firing.length === 0;
  const channels = useAlertChannels();
  const noChannels = !channels.loading && !channels.error && channels.channels.length === 0;

  return (
    <div className="space-y-6">
      <PageHeader
        title="Inbox"
        subtitle="Alerts from every server."
        actions={
          <>
            {count > 0 ? (
              <Button variant="outline" size="sm" className="max-sm:h-10" onClick={markAllRead}>
                Mark all as read
              </Button>
            ) : null}
            <Button asChild variant="ghost" size="sm" className="max-sm:h-10">
              <Link to={settingsPath("notifications")}>
                Notification settings
                <ArrowRight className="size-4" aria-hidden />
              </Link>
            </Button>
          </>
        }
      />

      {noChannels ? (
        <p className="text-sm text-muted-foreground">
          Alerts only show here.{" "}
          <Link to={settingsPath("notifications")} className="font-medium text-foreground underline-offset-4 hover:underline">
            Add a notification channel →
          </Link>
        </p>
      ) : null}

      <div className="flex flex-wrap items-center gap-2">
        <ToggleGroup
          type="single"
          size="sm"
          value={show}
          // Clicking the selected filter would unset it; the value stays.
          onValueChange={(v) => v && setShow(v as EventKindFilter)}
          aria-label="Show"
          className="gap-0 rounded-lg bg-muted p-[3px]"
        >
          {SHOW.map((o) => (
            <ToggleGroupItem key={o.value} value={o.value} className={FILTER_ITEM}>
              {o.label}
            </ToggleGroupItem>
          ))}
        </ToggleGroup>
        {serverFilter ? (
          <Select value={serverId ?? ALL_SERVERS} onValueChange={setPicked}>
            <SelectTrigger className="h-10 w-44 sm:h-9" aria-label="Server">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={ALL_SERVERS}>All servers</SelectItem>
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

      {firing.length > 0 ? (
        <Section title="Firing now">
          <RowList label="Firing now">
            {firing.map((a) => (
              <FiringRow key={`${a.rule_id}-${a.server_id}-${a.app_id}`} alert={a} now={now} />
            ))}
          </RowList>
        </Section>
      ) : null}

      {quiet ? <EmptyState icon={InboxIcon} title={QUIET} /> : null}

      {groups.map((g) => (
        <Section key={g.key} title={g.label}>
          <RowList label={g.label}>
            {g.items.map((ev) => (
              <EventRow key={ev.id} event={ev} unread={ev.kind === "fired" && isUnread(ev.created_at)} />
            ))}
          </RowList>
        </Section>
      ))}

      {hasMore ? (
        <Button variant="outline" size="sm" className="max-sm:h-10" onClick={loadMore} disabled={loading}>
          Load more
        </Button>
      ) : null}
    </div>
  );
}
