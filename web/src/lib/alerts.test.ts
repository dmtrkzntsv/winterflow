import { describe, expect, it } from "vitest";

import {
  ALERT_METRICS,
  buildChannel,
  FieldError,
  channelService,
  alertTargetPath,
  channelSummary,
  effective,
  formatAlertValue,
  routingText,
  ruleSummary,
  SERVICES,
  serviceSpec,
  type Channel,
  type Rule,
} from "@/lib/alerts";

const rule: Rule = {
  id: "r1",
  target_kind: "server",
  metric: "cpu",
  threshold: 90,
  duration_minutes: 10,
  channel_ids: null,
  enabled: true,
  created_at: "2026-10-11T12:00:00Z",
};

describe("alert wording", () => {
  it("mirrors the Go catalog", () => {
    expect(ALERT_METRICS).toHaveLength(13);
  });

  it("summarises rules", () => {
    expect(ruleSummary(rule)).toBe("CPU above 90% for 10 min");
    expect(ruleSummary({ ...rule, metric: "down", duration_minutes: 5 })).toBe("Server down for 5 min");
    expect(ruleSummary({ ...rule, target_kind: "app", metric: "disk", threshold: 50 })).toBe("Disk above 50.0 GB for 10 min");
    expect(ruleSummary({ ...rule, metric: "load5", threshold: 4 })).toBe("Load 5m above 4.00 for 10 min");
  });

  it("formats values like the notifications", () => {
    expect(formatAlertValue("%", 93.4)).toBe("93%");
    expect(formatAlertValue("MB/s", 12.44)).toBe("12.4 MB/s");
    expect(formatAlertValue("°C", 70.6)).toBe("71 °C");
  });

  it("words routing", () => {
    const channels = [{ id: "c1", name: "Ops" }] as Channel[];
    expect(routingText(null, channels)).toBe("Default channels");
    expect(routingText([], channels)).toBe("Inbox only");
    expect(routingText(["c1", "gone"], channels)).toBe("Ops, deleted channel");
  });

  it("merges overrides like the API", () => {
    expect(effective(rule, undefined)).toEqual({ enabled: true, threshold: 90, duration_minutes: 10, channel_ids: null });
    const o = { rule_id: "r1", server_id: "s", app_id: "", disabled: false, threshold: 95, duration_minutes: null, channel_ids: [] };
    expect(effective(rule, o)).toEqual({ enabled: true, threshold: 95, duration_minutes: 10, channel_ids: [] });
    expect(effective(rule, { ...o, disabled: true }).enabled).toBe(false);
  });
});

describe("channel URL builders", () => {
  it("builds every service's Shoutrrr URL", () => {
    const cases: [string, Record<string, string>, string][] = [
      ["telegram", { token: "123456:ABC-def_9", chats: "-100123, @ops " }, "telegram://123456:ABC-def_9@telegram?chats=-100123,@ops"],
      ["discord", { webhook: "https://discord.com/api/webhooks/42/tok-EN_x" }, "discord://tok-EN_x@42"],
      ["slack", { webhook: "https://hooks.slack.com/services/T1/B2/xyz" }, "slack://hook:T1-B2-xyz@webhook"],
      ["ntfy", { server: "ntfy.sh", topic: "my alerts" }, "ntfy://ntfy.sh/my%20alerts"],
      ["ntfy", { server: "http://ntfy.lan:8080/", topic: "t", username: "me", password: "p@ss" }, "ntfy://me:p%40ss@ntfy.lan:8080/t?scheme=http"],
      ["gotify", { server: "https://gotify.example.com", token: "AbC.123" }, "gotify://gotify.example.com/AbC.123"],
      ["pushover", { token: "tok", user: "usr", devices: "phone, tablet" }, "pushover://shoutrrr:tok@usr/?devices=phone,tablet"],
      [
        "email",
        { host: "smtp.example.com", port: "587", username: "me", password: "pw", from: "wf@example.com", to: "a@example.com, b@example.com" },
        "smtp://me:pw@smtp.example.com:587/?fromaddress=wf%40example.com&toaddresses=a%40example.com%2Cb%40example.com",
      ],
      ["email", { host: "mail.lan", port: "25", from: "wf@lan", to: "ops@lan" }, "smtp://mail.lan:25/?fromaddress=wf%40lan&toaddresses=ops%40lan&auth=None"],
      ["webhook", { url: "https://hooks.example.com/a/b?k=v" }, "generic://hooks.example.com/a/b?template=json&k=v"],
      ["webhook", { url: "http://10.0.0.2:9000/hook" }, "generic://10.0.0.2:9000/hook?template=json&disabletls=yes"],
      ["raw", { url: "matrix://u:p@host/?rooms=!r" }, "matrix://u:p@host/?rooms=!r"],
    ];
    for (const [id, values, want] of cases) {
      expect(buildChannel(id as never, values).url, id).toBe(want);
    }
  });

  it("keeps only non-secret fields, tagged with the form", () => {
    const { fields } = buildChannel("ntfy", { server: "ntfy.sh", topic: "secret-topic", username: "me", password: "pw" });
    expect(fields).toEqual({ ui_service: "ntfy", server: "ntfy.sh", username: "me" });
  });

  it("explains bad input", () => {
    expect(() => buildChannel("telegram", { token: "nope", chats: "1" })).toThrow(/123456789:ABC/);
    expect(() => buildChannel("telegram", { token: "1:a" })).toThrow("Chats is required");
    expect(() => buildChannel("discord", { webhook: "https://example.com/x" })).toThrow(/Discord webhook/);
    expect(() => buildChannel("email", { host: "h", port: "x", from: "a", to: "b" })).toThrow(/Port/);
    expect(() => buildChannel("webhook", { url: "ftp://x" })).toThrow(/https/);
    expect(() => buildChannel("webhook", { url: "https://me:pw@hooks.example.com/a" })).toThrow(/Credentials in the URL are not supported/);
    expect(() => buildChannel("webhook", { url: "https://token@hooks.example.com/a" })).toThrow(/Credentials/);
    expect(() => buildChannel("ntfy", { server: "ntfy.sh" })).toThrow("Topic is required");
    expect(() => buildChannel("email", { host: "h", port: "x", from: "a", to: "b" })).toThrow(
      expect.objectContaining({ field: "port" }),
    );
    expect(() => buildChannel("ntfy", { server: "ntfy.sh" })).toThrow(FieldError);
    expect(() => buildChannel("webhook", { url: "https://me:pw@hooks.example.com/a" })).toThrow(
      /Other \(URL\) service/,
    );
  });

  it("recognises a stored channel's form", () => {
    expect(channelService({ service: "generic", fields: {} })).toBe("webhook");
    expect(channelService({ service: "smtp", fields: null })).toBe("email");
    expect(channelService({ service: "matrix", fields: {} })).toBe("raw");
    expect(channelService({ service: "generic", fields: { ui_service: "raw" } })).toBe("raw");
    expect(channelSummary({ service: "ntfy", fields: { ui_service: "ntfy", server: "ntfy.sh" } })).toBe("ntfy · ntfy.sh");
    expect(SERVICES.map((s) => s.id)).toEqual(["telegram", "discord", "slack", "ntfy", "gotify", "pushover", "email", "webhook", "raw"]);
  });
});

describe("alertTargetPath", () => {
  it("leads an app's alert to its Metrics tab and a server's to its Monitoring", () => {
    expect(alertTargetPath("s1", "a1")).toBe("/server/s1/app/a1?tab=metrics");
    expect(alertTargetPath("s1", "")).toBe("/server/s1/monitoring");
  });
});

describe("channel form wording", () => {
  it("calls a raw Shoutrrr URL Other (URL) and folds SMTP's port and sign-in, and ntfy's sign-in, under Advanced", () => {
    expect(serviceSpec("raw").label).toBe("Other (URL)");
    expect(serviceSpec("email").fields.filter((f) => f.advanced).map((f) => f.key)).toEqual(["port", "username", "password"]);
    expect(serviceSpec("ntfy").fields.filter((f) => f.advanced).map((f) => f.key)).toEqual(["username", "password"]);
    expect(SERVICES.filter((s) => s.id !== "email" && s.id !== "ntfy").every((s) => s.fields.every((f) => !f.advanced))).toBe(true);
  });
});
