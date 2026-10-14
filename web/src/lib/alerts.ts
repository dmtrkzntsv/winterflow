// Alerting model and pure helpers: the metric catalog (mirrors
// internal/domain/alert/catalog.go), API types, rule and value wording,
// and the notification services the channel form turns into Shoutrrr URLs.

import { appPath, monitoringPath } from "@/lib/routes";

export type TargetKind = "server" | "app";

export type AlertMetric =
  | "down"
  | "cpu"
  | "memory"
  | "disk"
  | "load1"
  | "load5"
  | "load15"
  | "bandwidth"
  | "temperature";

export type AlertUnit = "" | "%" | "load" | "MB/s" | "°C" | "GB";

export type MetricInfo = {
  kind: TargetKind;
  metric: AlertMetric;
  label: string;
  unit: AlertUnit;
  // max bounds a threshold (0 = unbounded); thresholds are > 0.
  max: number;
  // suggested threshold for a new rule.
  suggested: number;
};

export const ALERT_METRICS: MetricInfo[] = [
  { kind: "server", metric: "down", label: "Down", unit: "", max: 0, suggested: 0 },
  { kind: "server", metric: "cpu", label: "CPU", unit: "%", max: 100, suggested: 90 },
  { kind: "server", metric: "memory", label: "Memory", unit: "%", max: 100, suggested: 90 },
  { kind: "server", metric: "disk", label: "Disk", unit: "%", max: 100, suggested: 90 },
  { kind: "server", metric: "load1", label: "Load 1m", unit: "load", max: 0, suggested: 4 },
  { kind: "server", metric: "load5", label: "Load 5m", unit: "load", max: 0, suggested: 4 },
  { kind: "server", metric: "load15", label: "Load 15m", unit: "load", max: 0, suggested: 4 },
  { kind: "server", metric: "bandwidth", label: "Bandwidth", unit: "MB/s", max: 0, suggested: 50 },
  { kind: "server", metric: "temperature", label: "Temperature", unit: "°C", max: 150, suggested: 80 },
  { kind: "app", metric: "down", label: "Down", unit: "", max: 0, suggested: 0 },
  { kind: "app", metric: "cpu", label: "CPU", unit: "%", max: 100, suggested: 80 },
  { kind: "app", metric: "memory", label: "Memory", unit: "%", max: 100, suggested: 90 },
  { kind: "app", metric: "disk", label: "Disk", unit: "GB", max: 0, suggested: 50 },
];

export function metricInfo(kind: TargetKind, metric: AlertMetric): MetricInfo | undefined {
  return ALERT_METRICS.find((m) => m.kind === kind && m.metric === metric);
}

export function hasThreshold(metric: AlertMetric): boolean {
  return metric !== "down";
}

// API types (internal/domain/alert, handler/alerts).

export type Channel = {
  id: string;
  name: string;
  service: string;
  fields: Record<string, string> | null;
  is_default: boolean;
  enabled: boolean;
  last_error: string;
  last_sent_at: string | null;
  created_at: string;
};

// channel_ids: null = the org's default channels, [] = none (History only).
export type Rule = {
  id: string;
  target_kind: TargetKind;
  metric: AlertMetric;
  threshold: number;
  duration_minutes: number;
  channel_ids: string[] | null;
  enabled: boolean;
  created_at: string;
};

// Null fields inherit the rule's; channel_ids null inherits, [] silences.
export type Override = {
  rule_id: string;
  server_id: string;
  app_id: string;
  disabled: boolean;
  threshold: number | null;
  duration_minutes: number | null;
  channel_ids: string[] | null;
};

export type Delivery = { channel_id: string; channel_name: string; ok: boolean; error: string };

export type AlertEvent = {
  id: string;
  rule_id: string;
  server_id: string;
  app_id: string;
  server_name: string;
  app_name: string;
  target_kind: TargetKind;
  metric: AlertMetric;
  kind: "fired" | "resolved";
  value: number;
  threshold: number;
  title: string;
  message: string;
  deliveries: Delivery[] | null;
  created_at: string;
};

export type ActiveAlert = {
  rule_id: string;
  target_kind: TargetKind;
  metric: AlertMetric;
  server_id: string;
  app_id: string;
  since: string;
  value: number;
  threshold: number;
  server_name: string;
  app_name: string;
};

// AlertNotice is the payload of an SSE "alert" notification.
export type AlertNotice = {
  kind: "fired" | "resolved";
  rule_id: string;
  target_kind: TargetKind;
  metric: AlertMetric;
  server_id: string;
  app_id: string;
  value: number;
  threshold: number;
  title: string;
};

export const ALERT_NOTIFICATION = "alert";

// formatAlertValue renders a value in its metric's unit, as the
// notifications do ("93%", "2.31", "12.4 MB/s", "71 °C", "14.2 GB").
export function formatAlertValue(unit: AlertUnit, v: number): string {
  switch (unit) {
    case "%":
      return `${Math.round(v)}%`;
    case "load":
      return v.toFixed(2);
    case "MB/s":
      return `${v.toFixed(1)} MB/s`;
    case "°C":
      return `${Math.round(v)} °C`;
    case "GB":
      return `${v.toFixed(1)} GB`;
    default:
      return String(v);
  }
}

// conditionText words a rule's condition: "above 90% for 10 min" /
// "down for 5 min".
export function conditionText(metric: AlertMetric, unit: AlertUnit, threshold: number, minutes: number): string {
  const span = `for ${minutes} min`;
  return hasThreshold(metric) ? `above ${formatAlertValue(unit, threshold)} ${span}` : `down ${span}`;
}

// ruleSummary is a one-line description: "CPU above 90% for 10 min".
export function ruleSummary(rule: Pick<Rule, "target_kind" | "metric" | "threshold" | "duration_minutes">): string {
  const info = metricInfo(rule.target_kind, rule.metric);
  if (!info) return `${rule.metric} (unknown)`;
  if (!hasThreshold(rule.metric)) {
    return `${rule.target_kind === "app" ? "App" : "Server"} ${conditionText(rule.metric, info.unit, 0, rule.duration_minutes)}`;
  }
  return `${info.label} ${conditionText(rule.metric, info.unit, rule.threshold, rule.duration_minutes)}`;
}

// routingText words where a rule's notifications go.
export function routingText(ids: string[] | null, channels: Channel[]): string {
  if (ids === null) return "Default channels";
  if (ids.length === 0) return "History only";
  const names = ids.map((id) => channels.find((c) => c.id === id)?.name ?? "deleted channel");
  return names.join(", ");
}

export type Routing = "default" | "specific" | "none";

// routingOf names how a channel list routes: null = defaults, [] = none.
export function routingOf(ids: string[] | null): Routing {
  if (ids === null) return "default";
  return ids.length === 0 ? "none" : "specific";
}

// effective merges a rule with a target's override, as the API does.
export function effective(rule: Rule, o: Override | undefined) {
  return {
    enabled: rule.enabled && !o?.disabled,
    threshold: o?.threshold ?? rule.threshold,
    duration_minutes: o?.duration_minutes ?? rule.duration_minutes,
    channel_ids: o && o.channel_ids !== null ? o.channel_ids : rule.channel_ids,
  };
}

// Notification services: each form builds a Shoutrrr URL. Secret fields
// never leave the browser except inside the URL (stored encrypted); the
// other fields are kept with the channel to show and prefill the form.

export type ServiceId =
  | "telegram"
  | "discord"
  | "slack"
  | "ntfy"
  | "gotify"
  | "pushover"
  | "email"
  | "webhook"
  | "raw";

export type FieldSpec = {
  key: string;
  label: string;
  secret?: boolean;
  optional?: boolean;
  placeholder?: string;
  hint?: string;
  defaultValue?: string;
};

export type ServiceSpec = {
  id: ServiceId;
  label: string;
  fields: FieldSpec[];
  // build turns the form values into a Shoutrrr URL or throws an Error
  // whose message is shown under the form.
  build: (f: Record<string, string>) => string;
};

const enc = encodeURIComponent;

function req(f: Record<string, string>, key: string, label: string): string {
  const v = (f[key] ?? "").trim();
  if (!v) throw new Error(`${label} is required`);
  return v;
}

function opt(f: Record<string, string>, key: string): string {
  return (f[key] ?? "").trim();
}

// splitHost turns "https://host:port/path" or "host/path" into the
// scheme-less host+path and whether plain HTTP was asked for.
function splitHost(raw: string): { hostPath: string; insecure: boolean } {
  const insecure = /^http:\/\//i.test(raw);
  const hostPath = raw.replace(/^https?:\/\//i, "").replace(/\/+$/, "");
  if (!hostPath || /\s/.test(hostPath)) throw new Error("Server must be a host name, like ntfy.example.com");
  return { hostPath, insecure };
}

function list(v: string): string {
  return v
    .split(",")
    .map((s) => s.trim())
    .filter(Boolean)
    .join(",");
}

export const SERVICES: ServiceSpec[] = [
  {
    id: "telegram",
    label: "Telegram",
    fields: [
      { key: "token", label: "Bot token", secret: true, placeholder: "123456789:ABC-DEF…", hint: "From @BotFather." },
      { key: "chats", label: "Chats", placeholder: "-1001234567890, @mychannel", hint: "Chat IDs or @channel names, comma-separated." },
    ],
    build: (f) => {
      const token = req(f, "token", "Bot token");
      if (!/^\d+:[\w-]+$/.test(token)) throw new Error("Bot token looks like 123456789:ABC-DEF…");
      return `telegram://${token}@telegram?chats=${list(req(f, "chats", "Chats"))}`;
    },
  },
  {
    id: "discord",
    label: "Discord",
    fields: [
      {
        key: "webhook",
        label: "Webhook URL",
        secret: true,
        placeholder: "https://discord.com/api/webhooks/…",
        hint: "Channel settings → Integrations → Webhooks → Copy Webhook URL.",
      },
    ],
    build: (f) => {
      const m = req(f, "webhook", "Webhook URL").match(/discord(?:app)?\.com\/api\/webhooks\/(\d+)\/([\w-]+)/);
      if (!m) throw new Error("Paste the whole Discord webhook URL");
      return `discord://${m[2]}@${m[1]}`;
    },
  },
  {
    id: "slack",
    label: "Slack",
    fields: [
      {
        key: "webhook",
        label: "Webhook URL",
        secret: true,
        placeholder: "https://hooks.slack.com/services/T…/B…/…",
        hint: "An Incoming Webhook of your Slack app.",
      },
    ],
    build: (f) => {
      const m = req(f, "webhook", "Webhook URL").match(/hooks\.slack\.com\/services\/([^/]+)\/([^/]+)\/([^/?#]+)/);
      if (!m) throw new Error("Paste the whole Slack webhook URL");
      return `slack://hook:${m[1]}-${m[2]}-${m[3]}@webhook`;
    },
  },
  {
    id: "ntfy",
    label: "ntfy",
    fields: [
      { key: "server", label: "Server", defaultValue: "ntfy.sh", placeholder: "ntfy.sh" },
      { key: "topic", label: "Topic", secret: true, placeholder: "my-alerts-7f3k", hint: "Anyone who knows a public topic can read it." },
      { key: "username", label: "Username", optional: true },
      { key: "password", label: "Password or token", secret: true, optional: true },
    ],
    build: (f) => {
      const { hostPath, insecure } = splitHost(req(f, "server", "Server"));
      const topic = req(f, "topic", "Topic");
      const user = opt(f, "username");
      const pass = opt(f, "password");
      const auth = user || pass ? `${enc(user)}:${enc(pass)}@` : "";
      return `ntfy://${auth}${hostPath}/${enc(topic)}${insecure ? "?scheme=http" : ""}`;
    },
  },
  {
    id: "gotify",
    label: "Gotify",
    fields: [
      { key: "server", label: "Server", placeholder: "gotify.example.com" },
      { key: "token", label: "App token", secret: true, placeholder: "AzyoeNS.D4iJLVa" },
    ],
    build: (f) => {
      const { hostPath, insecure } = splitHost(req(f, "server", "Server"));
      return `gotify://${hostPath}/${enc(req(f, "token", "App token"))}${insecure ? "?disabletls=yes" : ""}`;
    },
  },
  {
    id: "pushover",
    label: "Pushover",
    fields: [
      { key: "token", label: "API token", secret: true },
      { key: "user", label: "User key", secret: true },
      { key: "devices", label: "Devices", optional: true, hint: "Comma-separated; empty sends to all." },
    ],
    build: (f) => {
      const devices = list(opt(f, "devices"));
      return `pushover://shoutrrr:${enc(req(f, "token", "API token"))}@${enc(req(f, "user", "User key"))}/${devices ? `?devices=${devices}` : ""}`;
    },
  },
  {
    id: "email",
    label: "Email (SMTP)",
    fields: [
      { key: "host", label: "SMTP server", placeholder: "smtp.example.com" },
      { key: "port", label: "Port", defaultValue: "587" },
      { key: "username", label: "Username", optional: true },
      { key: "password", label: "Password", secret: true, optional: true },
      { key: "from", label: "From", placeholder: "winterflow@example.com" },
      { key: "to", label: "To", placeholder: "ops@example.com", hint: "Comma-separated." },
    ],
    build: (f) => {
      const host = req(f, "host", "SMTP server");
      const port = req(f, "port", "Port");
      if (!/^\d{1,5}$/.test(port)) throw new Error("Port must be a number");
      const user = opt(f, "username");
      const pass = opt(f, "password");
      const auth = user ? `${enc(user)}:${enc(pass)}@` : "";
      const query = `fromaddress=${enc(req(f, "from", "From"))}&toaddresses=${enc(list(req(f, "to", "To")))}`;
      return `smtp://${auth}${host}:${port}/?${query}${user ? "" : "&auth=None"}`;
    },
  },
  {
    id: "webhook",
    label: "Webhook",
    fields: [
      {
        key: "url",
        label: "URL",
        secret: true,
        placeholder: "https://example.com/hooks/winterflow",
        hint: "Receives a JSON POST with title and message.",
      },
    ],
    build: (f) => {
      const raw = req(f, "url", "URL");
      let u: URL;
      try {
        u = new URL(raw);
      } catch {
        throw new Error("URL must start with https:// or http://");
      }
      if (u.protocol !== "https:" && u.protocol !== "http:") throw new Error("URL must start with https:// or http://");
      if (u.username || u.password) {
        throw new Error("Credentials in the URL are not supported; use a header-less token in the path or the raw Shoutrrr URL");
      }
      const extra = u.search ? `&${u.search.slice(1)}` : "";
      return `generic://${u.host}${u.pathname}?template=json${u.protocol === "http:" ? "&disabletls=yes" : ""}${extra}`;
    },
  },
  {
    id: "raw",
    label: "Shoutrrr URL",
    fields: [
      {
        key: "url",
        label: "URL",
        secret: true,
        placeholder: "matrix://user:password@host/?rooms=!room",
        hint: "Any Shoutrrr service URL (containrrr.dev/shoutrrr).",
      },
    ],
    build: (f) => req(f, "url", "URL"),
  },
];

export function serviceSpec(id: ServiceId): ServiceSpec {
  return SERVICES.find((s) => s.id === id) ?? SERVICES[SERVICES.length - 1];
}

// UI_SERVICE_FIELD names the stored field holding the form's service.
export const UI_SERVICE_FIELD = "ui_service";

// channelService is the form a stored channel was made with: its stored
// ui_service, else a guess from the URL scheme.
export function channelService(ch: Pick<Channel, "service" | "fields">): ServiceId {
  const stored = ch.fields?.[UI_SERVICE_FIELD];
  if (stored && SERVICES.some((s) => s.id === stored)) return stored as ServiceId;
  const byScheme: Record<string, ServiceId> = {
    telegram: "telegram",
    discord: "discord",
    slack: "slack",
    ntfy: "ntfy",
    gotify: "gotify",
    pushover: "pushover",
    smtp: "email",
    generic: "webhook",
  };
  return byScheme[ch.service] ?? "raw";
}

// buildChannel turns a service form into the URL and the non-secret
// fields to store with it.
export function buildChannel(
  id: ServiceId,
  values: Record<string, string>,
): { url: string; fields: Record<string, string> } {
  const spec = serviceSpec(id);
  const url = spec.build(values);
  const fields: Record<string, string> = { [UI_SERVICE_FIELD]: id };
  for (const f of spec.fields) {
    const v = (values[f.key] ?? "").trim();
    if (!f.secret && v) fields[f.key] = v;
  }
  return { url, fields };
}

// channelSummary is the public part of a channel's destination.
export function channelSummary(ch: Pick<Channel, "service" | "fields">): string {
  const id = channelService(ch);
  const spec = serviceSpec(id);
  const shown = spec.fields
    .filter((f) => !f.secret && ch.fields?.[f.key])
    .map((f) => ch.fields?.[f.key] as string);
  return [spec.label, ...shown].join(" · ");
}

// sinceText is how long ago an alert started: "just now", "12 min",
// "3 h", "2 d".
export function sinceText(since: string, now: number): string {
  const mins = Math.max(0, Math.floor((now - Date.parse(since)) / 60_000));
  if (mins < 1) return "just now";
  if (mins < 60) return `${mins} min`;
  const hours = Math.floor(mins / 60);
  if (hours < 48) return `${hours} h`;
  return `${Math.floor(hours / 24)} d`;
}

// activeText describes a firing alert: "CPU 93% > 90%" / "Down".
export function activeText(a: Pick<ActiveAlert, "target_kind" | "metric" | "value" | "threshold">): string {
  const info = metricInfo(a.target_kind, a.metric);
  if (!info || !hasThreshold(a.metric)) return "Down";
  return `${info.label} ${formatAlertValue(info.unit, a.value)} > ${formatAlertValue(info.unit, a.threshold)}`;
}

// alertTargetPath is where an alert leads: an app's Metrics tab, or the
// server's Monitoring page for an alert about the server itself.
export function alertTargetPath(serverId: string, appId: string): string {
  return appId ? appPath(serverId, appId, "metrics") : monitoringPath(serverId);
}
