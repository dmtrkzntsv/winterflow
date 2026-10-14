import { describe, expect, it } from "vitest";

import {
  activeTitle,
  alertTitle,
  dayLabel,
  deliveryNote,
  eventTitle,
  groupByDay,
  targetText,
  type AlertSubject,
} from "@/lib/alert-text";
import type { ActiveAlert, AlertEvent, AlertMetric } from "@/lib/alerts";

const server: AlertSubject = { target_kind: "server", metric: "cpu", server_name: "homelab", app_name: "", value: 93.4, threshold: 90 };
const app: AlertSubject = { target_kind: "app", metric: "down", server_name: "homelab", app_name: "Website", value: 0, threshold: 0 };

const event = (over: Partial<AlertEvent> = {}): AlertEvent => ({
  id: "e1",
  rule_id: "r1",
  server_id: "s1",
  app_id: "",
  server_name: "homelab",
  app_name: "",
  target_kind: "server",
  metric: "cpu",
  kind: "fired",
  value: 93,
  threshold: 90,
  title: "🔥 homelab · CPU 93% > 90% for 10m",
  message: "",
  deliveries: [],
  created_at: "2026-10-13T10:00:00Z",
  ...over,
});

describe("alertTitle", () => {
  it("says what crossed which line, in plain words", () => {
    expect(alertTitle("fired", server)).toBe("homelab CPU above 90%");
    expect(alertTitle("resolved", { ...server, value: 41 })).toBe("homelab CPU back to 41%");
    expect(alertTitle("fired", { ...server, metric: "memory" })).toBe("homelab memory above 90%");
    expect(alertTitle("fired", { ...server, metric: "load1", threshold: 4 })).toBe("homelab load 1m above 4.00");
    expect(alertTitle("fired", { ...app, metric: "disk", threshold: 50 })).toBe("Website disk above 50.0 GB");
  });

  it("says down and back up for availability", () => {
    expect(alertTitle("fired", app)).toBe("Website is down");
    expect(alertTitle("resolved", app)).toBe("Website is back up");
    expect(alertTitle("fired", { ...server, metric: "down" })).toBe("homelab is down");
  });

  it("names nameless targets", () => {
    expect(alertTitle("fired", { ...app, app_name: "  " })).toBe("An app is down");
    expect(alertTitle("fired", { ...server, server_name: "" })).toBe("A server CPU above 90%");
  });

  it("falls back on an unknown metric", () => {
    const metric = "gpu" as AlertMetric;
    expect(alertTitle("fired", { ...server, metric }, "🔥 homelab · GPU 93% > 90% for 10m")).toBe(
      "homelab · GPU 93% > 90% for 10m",
    );
    expect(alertTitle("fired", { ...server, metric })).toBe("homelab: gpu alert");
  });

  it("titles history events and firing alerts", () => {
    expect(eventTitle(event({ kind: "resolved", value: 41 }))).toBe("homelab CPU back to 41%");
    const firing: ActiveAlert = { rule_id: "r1", since: "", server_id: "s1", app_id: "", ...server };
    expect(activeTitle(firing)).toBe("homelab CPU above 90%");
  });
});

describe("targetText", () => {
  it("is the server, or the app on its server", () => {
    expect(targetText(server)).toBe("homelab");
    expect(targetText(app)).toBe("Website on homelab");
    expect(targetText({ ...app, app_name: "", server_name: "" })).toBe("An app on a server");
  });
});

describe("deliveryNote", () => {
  it("says where the alert went", () => {
    expect(deliveryNote(null)).toEqual({ text: "Only in the Inbox", failed: false, detail: "" });
    expect(deliveryNote([])).toEqual({ text: "Only in the Inbox", failed: false, detail: "" });
    expect(deliveryNote([{ channel_id: "c1", channel_name: "Telegram", ok: true, error: "" }])).toEqual({
      text: "Sent to Telegram",
      failed: false,
      detail: "",
    });
  });

  it("names the channels it could not reach, with the reasons as detail", () => {
    expect(
      deliveryNote([
        { channel_id: "c1", channel_name: "Ops", ok: true, error: "" },
        { channel_id: "c2", channel_name: "Mail", ok: false, error: "535 auth failed" },
      ]),
    ).toEqual({ text: "Sent to Ops · Couldn't send to Mail", failed: true, detail: "Mail: 535 auth failed" });
  });

  it("says a failure without a reason is a failure", () => {
    expect(
      deliveryNote([
        { channel_id: "c2", channel_name: "Mail", ok: false, error: "" },
        { channel_id: "c3", channel_name: "Ops", ok: false, error: "  " },
      ]).detail,
    ).toBe("Mail: failed\nOps: failed");
  });
});

describe("day groups", () => {
  // Local times: the groups follow the browser's day, whatever its zone.
  const now = new Date(2026, 9, 13, 12, 0).getTime();
  const at = (day: number, hour: number) => new Date(2026, 9, day, hour, 0).toISOString();
  const longDate = (iso: string, year = false) =>
    new Date(iso).toLocaleDateString(undefined, {
      weekday: "long",
      month: "long",
      day: "numeric",
      ...(year ? { year: "numeric" } : {}),
    });

  it("labels today, yesterday, and older days by date", () => {
    expect(dayLabel(at(13, 0), now)).toBe("Today");
    expect(dayLabel(at(13, 23), now)).toBe("Today");
    expect(dayLabel(at(12, 23), now)).toBe("Yesterday");
    expect(dayLabel(at(12, 0), now)).toBe("Yesterday");
    expect(dayLabel(at(10, 9), now)).toBe(longDate(at(10, 9)));
    const lastYear = new Date(2025, 11, 31, 9).toISOString();
    expect(dayLabel(lastYear, now)).toBe(longDate(lastYear, true));
  });

  it("dates an event stamped ahead of this browser's day instead of calling it today", () => {
    expect(dayLabel(at(14, 1), now)).toBe(longDate(at(14, 1)));
  });

  it("reads a broken timestamp as an unknown date", () => {
    expect(dayLabel("soon", now)).toBe("Unknown date");
  });

  it("groups newest-first events by day, one group per day", () => {
    const items = [
      { id: "a", created_at: at(13, 11) },
      { id: "b", created_at: at(13, 9) },
      { id: "c", created_at: at(12, 22) },
      { id: "d", created_at: at(13, 8) }, // out of order: still today's group
    ];
    const groups = groupByDay(items, now);
    expect(groups.map((g) => g.label)).toEqual(["Today", "Yesterday"]);
    expect(groups[0].items.map((i) => i.id)).toEqual(["a", "b", "d"]);
    expect(new Set(groups.map((g) => g.key)).size).toBe(2);
  });
});
