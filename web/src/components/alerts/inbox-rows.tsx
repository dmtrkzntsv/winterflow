import type { ReactNode } from "react";
import { CircleCheck } from "lucide-react";

import { Row } from "@/components/section";
import { StatusDot } from "@/components/status";
import { alertTargetPath, sinceText, type ActiveAlert, type AlertEvent } from "@/lib/alerts";
import { activeTitle, deliveryNote, eventTitle, targetText } from "@/lib/alert-text";
import { formatClock } from "@/lib/kpi";
import { cn } from "@/lib/utils";

// The slot before the status mark holds the unread dot, so titles line up
// whether or not a row is unread.
function Lead({ unread, children }: { unread: boolean; children: ReactNode }) {
  return (
    <div className="flex items-center gap-2">
      <span className="flex w-2 shrink-0 items-center">
        {unread ? <span role="img" aria-label="Unread" className="size-2 rounded-full bg-primary" /> : null}
      </span>
      <span className="flex size-5 shrink-0 items-center justify-center">{children}</span>
    </div>
  );
}

// EventRow is one history event: unread dot (a firing newer than the last
// visit), red dot (fired) or green check (resolved), the plain title, then
// target and time, with where it was sent on the right. The title link
// covers the row and opens the target.
export function EventRow({ event, unread }: { event: AlertEvent; unread: boolean }) {
  const note = deliveryNote(event.deliveries);
  return (
    <Row
      testId="event-row"
      to={alertTargetPath(event.server_id, event.app_id)}
      leading={
        <Lead unread={unread}>
          {event.kind === "fired" ? (
            <StatusDot tone="bad" label="Fired" />
          ) : (
            <CircleCheck className="size-4 text-status-ok" aria-label="Resolved" />
          )}
        </Lead>
      }
      title={<span className={unread ? "font-semibold" : undefined}>{eventTitle(event)}</span>}
      description={
        <>
          {targetText(event)} <span aria-hidden>·</span>{" "}
          <time dateTime={event.created_at} className="tabular-nums">
            {formatClock(Date.parse(event.created_at))}
          </time>
        </>
      }
      // Above the row's link, so the failure reason shows on hover.
      actions={
        <span
          className={cn("text-xs", note.failed ? "text-status-bad" : "text-muted-foreground")}
          title={note.detail || undefined}
        >
          {note.text}
        </span>
      }
    />
  );
}

// FiringRow is an alert firing now: red dot, plain title, target and how
// long it has been firing.
export function FiringRow({ alert, now }: { alert: ActiveAlert; now: number }) {
  return (
    <Row
      testId="firing-row"
      to={alertTargetPath(alert.server_id, alert.app_id)}
      leading={
        <Lead unread={false}>
          <StatusDot tone="bad" label="Firing" />
        </Lead>
      }
      title={<span className="font-semibold">{activeTitle(alert)}</span>}
      description={
        <>
          {targetText(alert)} <span aria-hidden>·</span> <span className="tabular-nums">for {sinceText(alert.since, now)}</span>
        </>
      }
    />
  );
}
