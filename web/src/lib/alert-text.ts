import {
  formatAlertValue,
  hasThreshold,
  metricInfo,
  type ActiveAlert,
  type AlertEvent,
  type Delivery,
} from "@/lib/alerts";

// Plain wording of alerts for the Inbox and the firing banners (spec §1
// "Plain words", §5): "Website is down", "homelab CPU above 90%". The
// notifications themselves keep their emoji and operators
// ("🔥 homelab · CPU 93% > 90% for 10m").

export type AlertSubject = Pick<
  AlertEvent,
  "target_kind" | "metric" | "server_name" | "app_name" | "value" | "threshold"
>;

type Target = Pick<AlertEvent, "target_kind" | "server_name" | "app_name">;

function serverName(t: Target): string {
  return t.server_name.trim() || "A server";
}

// subjectName is what an alert is about: the app for an app alert, else the
// server. A target without a name (not reconciled yet, or since removed) is
// still a subject.
function subjectName(t: Target): string {
  return t.target_kind === "app" ? t.app_name.trim() || "An app" : serverName(t);
}

// metricWord is a metric's label inside a sentence: "CPU" stays, the others
// go lower-case ("memory", "load 1m").
function metricWord(label: string): string {
  return label === "CPU" ? label : label.charAt(0).toLowerCase() + label.slice(1);
}

// stripMarks drops the emoji and punctuation a notification title starts
// with ("🔥 ", "✅ ").
function stripMarks(title: string): string {
  return title.replace(/^[^\p{L}\p{N}]+/u, "");
}

// alertTitle is one alert transition as a sentence. A metric this UI does
// not know (a newer backend) falls back to the notification's own title.
export function alertTitle(kind: "fired" | "resolved", subject: AlertSubject, fallback = ""): string {
  const name = subjectName(subject);
  const info = metricInfo(subject.target_kind, subject.metric);
  if (!info) return stripMarks(fallback) || `${name}: ${subject.metric} alert`;
  if (!hasThreshold(subject.metric)) return kind === "fired" ? `${name} is down` : `${name} is back up`;
  const word = metricWord(info.label);
  return kind === "fired"
    ? `${name} ${word} above ${formatAlertValue(info.unit, subject.threshold)}`
    : `${name} ${word} back to ${formatAlertValue(info.unit, subject.value)}`;
}

// eventTitle titles a history event.
export function eventTitle(ev: AlertEvent): string {
  return alertTitle(ev.kind, ev, ev.title);
}

// activeTitle titles an alert that is firing now.
export function activeTitle(a: ActiveAlert): string {
  return alertTitle("fired", a);
}

// targetText says where an alert is: "homelab", or "Website on homelab".
export function targetText(t: Target): string {
  if (t.target_kind !== "app") return serverName(t);
  return `${subjectName(t)} on ${t.server_name.trim() || "a server"}`;
}

// placeText is where an alert is beyond what its title names: an app
// alert's server; nothing for a server alert (its title names the server).
export function placeText(t: Target): string {
  return t.target_kind === "app" ? t.server_name.trim() || "a server" : "";
}

export type DeliveryNote = {
  text: string;
  // failed is true when at least one channel could not be reached.
  failed: boolean;
  // detail is one "channel: error" line per failed channel (a tooltip).
  detail: string;
  // reason is the same in a line a phone can read: each channel with only
  // the cause at the end of its error ("Phone: connection refused").
  reason: string;
};

// cause is the last part of an error chain ("post …: dial tcp …: connection
// refused" → "connection refused").
function cause(error: string): string {
  const first = error.trim().split("\n")[0];
  const parts = first.split(": ");
  return parts[parts.length - 1] || "failed";
}

// deliveryNote is the small line saying where an event's notification went:
// "Sent to Telegram", "Sent to Ops · Couldn't send to Mail"; "" when it went
// nowhere (the Inbox says that once, not per row).
export function deliveryNote(deliveries: Delivery[] | null): DeliveryNote {
  const list = deliveries ?? [];
  if (list.length === 0) return { text: "", failed: false, detail: "", reason: "" };
  const sent = list.filter((d) => d.ok).map((d) => d.channel_name);
  const failed = list.filter((d) => !d.ok);
  const parts: string[] = [];
  if (sent.length > 0) parts.push(`Sent to ${sent.join(", ")}`);
  if (failed.length > 0) parts.push(`Couldn't send to ${failed.map((d) => d.channel_name).join(", ")}`);
  return {
    text: parts.join(" · "),
    failed: failed.length > 0,
    detail: failed.map((d) => `${d.channel_name}: ${d.error.trim() || "failed"}`).join("\n"),
    reason: failed.map((d) => `${d.channel_name}: ${cause(d.error)}`).join(" · "),
  };
}

function startOfDay(ms: number): number {
  const d = new Date(ms);
  d.setHours(0, 0, 0, 0);
  return d.getTime();
}

// dayLabel names an event's local day: "Today", "Yesterday", else its date
// ("Saturday, October 10", with the year when it is not this year's). A day
// after today (the server's clock ahead of this browser's) gets its date.
export function dayLabel(createdAt: string, now: number): string {
  const day = startOfDay(Date.parse(createdAt));
  if (!Number.isFinite(day)) return "Unknown date";
  const today = startOfDay(now);
  if (day === today) return "Today";
  // today - 1 ms is the last moment of yesterday, also across a DST change.
  if (day === startOfDay(today - 1)) return "Yesterday";
  const date = new Date(day);
  const sameYear = date.getFullYear() === new Date(now).getFullYear();
  return date.toLocaleDateString(undefined, {
    weekday: "long",
    month: "long",
    day: "numeric",
    ...(sameYear ? {} : { year: "numeric" }),
  });
}

export type DayGroup<T> = { key: string; label: string; items: T[] };

// groupByDay splits newest-first items into one group per local day, in the
// order the days first appear. Every item of a day lands in that day's single
// group, even when the list is not perfectly ordered.
export function groupByDay<T extends { created_at: string }>(items: T[], now: number): DayGroup<T>[] {
  const groups = new Map<number, DayGroup<T>>();
  for (const item of items) {
    const day = startOfDay(Date.parse(item.created_at));
    let group = groups.get(day);
    if (!group) {
      group = { key: String(day), label: dayLabel(item.created_at, now), items: [] };
      groups.set(day, group);
    }
    group.items.push(item);
  }
  return [...groups.values()];
}
