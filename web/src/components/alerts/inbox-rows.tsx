import type { ReactNode } from "react";
import { CircleAlert, CircleCheck } from "lucide-react";

import { Row } from "@/components/section";
import { StatusDot } from "@/components/status";
import { alertTargetPath, sinceText, type ActiveAlert, type AlertEvent } from "@/lib/alerts";
import { activeTitle, deliveryNote, eventTitle, placeText } from "@/lib/alert-text";
import { formatClock } from "@/lib/kpi";

// The slot at the far left holds the unread dot, so titles line up whether
// or not a row is unread.
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

// Where shows an alert's place beyond its title (an app alert's server)
// and a time, one "·" between them.
function Where({ place, children }: { place: string; children: ReactNode }) {
  return (
    <>
      {place ? (
        <>
          {place} <span aria-hidden>·</span>{" "}
        </>
      ) : null}
      {children}
    </>
  );
}

// EventRow is one history event: the unread dot at the far left (a firing
// newer than the last visit, with a bold title), a red alert mark (fired)
// or a green check (resolved) in one slot, the plain title (naming the app
// or server), the app's server and the time, and where it was sent on the
// right (nothing when it went nowhere: the page says that once). The title
// link covers the row and opens the target.
export function EventRow({ event, unread }: { event: AlertEvent; unread: boolean }) {
  const note = deliveryNote(event.deliveries);
  return (
    <Row
      testId="event-row"
      to={alertTargetPath(event.server_id, event.app_id)}
      leading={
        <Lead unread={unread}>
          {event.kind === "fired" ? (
            <CircleAlert role="img" className="size-4 text-status-bad" aria-label="Fired" />
          ) : (
            <CircleCheck role="img" className="size-4 text-status-ok" aria-label="Resolved" />
          )}
        </Lead>
      }
      title={<span className={unread ? "font-semibold" : undefined}>{eventTitle(event)}</span>}
      description={
        <>
          <Where place={placeText(event)}>
            <time dateTime={event.created_at} className="tabular-nums">
              {formatClock(Date.parse(event.created_at))}
            </time>
          </Where>
          {/* The reason is text, not only a tooltip, so a phone can read it. */}
          {note.failed ? (
            <span className="block whitespace-normal break-words" title={note.detail}>
              {note.reason}
            </span>
          ) : null}
        </>
      }
      // Above the row's link. A failure is marked by a red dot, the words
      // stay in the normal text colours.
      actions={
        note.text ? (
          <span
            className="inline-flex items-center gap-1.5 text-xs text-muted-foreground"
            title={note.detail || undefined}
          >
            {note.failed ? <StatusDot tone="bad" /> : null}
            {note.text}
          </span>
        ) : undefined
      }
    />
  );
}

// FiringRow is an alert firing now: red alert mark, plain title, the app's
// server and how long it has been firing.
export function FiringRow({ alert, now }: { alert: ActiveAlert; now: number }) {
  return (
    <Row
      testId="firing-row"
      to={alertTargetPath(alert.server_id, alert.app_id)}
      leading={
        <Lead unread={false}>
          <CircleAlert role="img" className="size-4 text-status-bad" aria-label="Firing" />
        </Lead>
      }
      title={<span className="font-semibold">{activeTitle(alert)}</span>}
      description={
        <>
          <Where place={placeText(alert)}>
            <span className="tabular-nums">for {sinceText(alert.since, now)}</span>
          </Where>
        </>
      }
    />
  );
}
