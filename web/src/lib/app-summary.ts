import { AppStatusCode, type App, type AppDomain } from "@/context/apps-context-base";
import { hasRunningContainers, type ContainerStatusWire } from "@/lib/app-status";
import type { BackupHealth } from "@/lib/backups";
import { coveringDomain, type RegisteredDomain } from "@/lib/domains";
import { isLoopback, type PublishedPort } from "@/lib/host-ports";
import type { AppTab } from "@/lib/routes";

// What the Apps page and the app page say about an app at a glance: where
// it lives, how many apps run, the one warning a card carries.

// AddressTLS is how an address is served, from the TLS mode of the server's
// registered domain that covers it (the route's own ssl flag is ignored by
// the proxy): "https" — the proxy serves HTTPS (auto or an uploaded
// certificate); "proxy" — plain HTTP behind the person's tunnel or proxy,
// which handles HTTPS; null — not known (no registration covers the host,
// or the server's domains aren't read: no built-in proxy, or not loaded).
export type AddressTLS = "https" | "proxy" | null;

// AppAddress is a public address of an app: the hostname (with the port
// for a published one), the URL that opens it ("" for a published port a
// browser can't open: a range, or udp) and how it is served; service names
// the compose service a published port belongs to.
export type AppAddress = { host: string; url: string; tls: AddressTLS; service?: string };

function addressOf(d: AppDomain, registrations: readonly RegisteredDomain[]): AppAddress {
  const covering = coveringDomain(d.domain, registrations);
  if (!covering) return { host: d.domain, url: `${d.ssl ? "https" : "http"}://${d.domain}`, tls: null };
  // A tunnel or proxy in front terminates TLS too: people reach it by https.
  return { host: d.domain, url: `https://${d.domain}`, tls: covering.tls === "off" ? "proxy" : "https" };
}

// portAddresses are the published host ports reachable from outside the
// server, as http://<ip>:<port>: a port bound to every interface opens on
// the server's IP (serverIp, the agent's server_ip), one bound to an
// address on that address; a loopback one is left out. None without an IP.
// A range is one address ("<ip>:8000–8010") and a udp port says so
// ("<ip>:51820/udp"); neither opens in a browser (no url).
export function portAddresses(ports: readonly PublishedPort[], serverIp: string | undefined): AppAddress[] {
  const out: AppAddress[] = [];
  const seen = new Set<string>();
  for (const p of ports) {
    if (isLoopback(p.host)) continue;
    const any = p.host === "" || p.host === "0.0.0.0" || p.host === "::";
    const ip = any ? (serverIp ?? "").trim() : p.host;
    if (!ip) continue;
    const at = `${ip.includes(":") ? `[${ip}]` : ip}:${p.port}`;
    const host = `${at}${p.last !== undefined ? `–${p.last}` : ""}${p.proto ? `/${p.proto}` : ""}`;
    if (seen.has(host)) continue;
    seen.add(host);
    const opens = p.last === undefined && !p.proto;
    out.push({ host, url: opens ? `http://${at}` : "", tls: null, service: p.service || undefined });
  }
  return out;
}

// appAddresses are the app's routes in the order the API lists them (a
// redirect is not where the app lives), then its published ports (see
// portAddresses). registrations are the server's registered domains
// (useAddressRegistrations).
export function appAddresses(
  app: Pick<App, "domains">,
  registrations: readonly RegisteredDomain[] = [],
  ports: readonly AppAddress[] = [],
): AppAddress[] {
  return [...(app.domains ?? []).filter((d) => d.kind === "route").map((d) => addressOf(d, registrations)), ...ports];
}

// primaryAddress is the address the UI opens ("Open"): the first domain,
// else the first published port a browser opens; null without one.
export function primaryAddress(
  app: Pick<App, "domains">,
  registrations: readonly RegisteredDomain[] = [],
  ports: readonly AppAddress[] = [],
): AppAddress | null {
  return appAddresses(app, registrations, ports).find((a) => a.url !== "") ?? null;
}

export type AppCounts = {
  running: number;
  failed: number;
  restarting: number;
  idle: number;
  problem: number;
  stopped: number;
};

// countApps sorts the apps by what their pills read: running (Active only),
// failed (a deploy that failed, see AppsContextValue.deployFailures),
// restarting, idle, with a problem, stopped. An app without a known status
// is in none (the UI never infers "down" from missing data).
export function countApps(
  apps: Pick<App, "id">[],
  statusByApp: Record<string, number>,
  failed: Record<string, string> = {},
): AppCounts {
  const counts: AppCounts = { running: 0, failed: 0, restarting: 0, idle: 0, problem: 0, stopped: 0 };
  for (const a of apps) {
    const code = statusByApp[a.id];
    if (failed[a.id]) counts.failed++;
    else if (code === AppStatusCode.Active) counts.running++;
    else if (code === AppStatusCode.Restarting) counts.restarting++;
    else if (code === AppStatusCode.Idle) counts.idle++;
    else if (code === AppStatusCode.Problematic) counts.problem++;
    else if (code === AppStatusCode.Stopped) counts.stopped++;
  }
  return counts;
}

// appsSubtitle is the Apps page's subtitle, counting apps (not containers):
// "homelab · 3 running · 1 failed · 1 stopped" (zero counts other than
// running are left out).
export function appsSubtitle(serverName: string, total: number, counts: AppCounts): string {
  const parts = [serverName];
  if (total === 0) {
    parts.push("no apps yet");
  } else {
    parts.push(`${counts.running} running`);
    if (counts.failed > 0) parts.push(`${counts.failed} failed`);
    if (counts.restarting > 0) parts.push(`${counts.restarting} restarting`);
    if (counts.idle > 0) parts.push(`${counts.idle} idle`);
    if (counts.problem > 0) parts.push(`${counts.problem} with problems`);
    if (counts.stopped > 0) parts.push(`${counts.stopped} stopped`);
  }
  return parts.join(" · ");
}

// AppWarning is the one small warning an app card shows, and the tab it
// leads to (none: the app page's default tab, whose banner explains it).
export type AppWarning = { label: string; tab?: AppTab };

// appWarning picks a card's warning: a failing backup, else a late one,
// else shared variables changed since the app last read them (only while it
// runs: a stopped app reads them on its next start). Backup health never
// changes the app's own status.
export function appWarning(
  health: BackupHealth[] | undefined,
  staleVars: string[] | undefined,
  status: number | undefined,
): AppWarning | null {
  const states = (health ?? []).map((h) => h.state);
  if (states.includes("failing")) return { label: "Backup failing", tab: "backups" };
  if (states.includes("late")) return { label: "Backup late", tab: "backups" };
  if ((staleVars ?? []).length > 0 && hasRunningContainers(status)) return { label: "Reload needed" };
  return null;
}

// FILTER_THRESHOLD is how many apps a server has before the Apps page
// offers a filter box.
export const FILTER_THRESHOLD = 8;

// filterApps keeps the apps whose name or an address contains the query
// (case-insensitive); an empty query keeps the list as it is.
export function filterApps<T extends Pick<App, "name" | "domains">>(apps: T[], query: string): T[] {
  const q = query.trim().toLowerCase();
  if (!q) return apps;
  return apps.filter(
    (a) => (a.name ?? "").toLowerCase().includes(q) || (a.domains ?? []).some((d) => d.domain.toLowerCase().includes(q)),
  );
}

// containerHint says in a few words what is wrong with a container ("" when
// nothing is): the agent's error (first line), else its exit code.
export function containerHint(c: ContainerStatusWire): string {
  if (c.status_code === AppStatusCode.Restarting) return "Keeps restarting";
  if (c.status_code !== AppStatusCode.Problematic) return "";
  const error = c.error?.trim().split("\n")[0];
  if (error) return error;
  return c.exit_code ? `Stopped with exit code ${c.exit_code}` : "Has a problem";
}

// serviceLabels names each container by its compose service: the project
// prefix (wf-<appId>-) and the replica suffix (-1) go, the suffix staying
// only where a service has several replicas. Keyed like the containers.
export function serviceLabels(appId: string, containers: Pick<ContainerStatusWire, "name">[]): string[] {
  const prefix = `wf-${appId}-`;
  const bases = containers.map((c) => (c.name.startsWith(prefix) ? c.name.slice(prefix.length) : c.name));
  const services = bases.map((b) => b.replace(/-\d+$/, ""));
  return bases.map((base, i) => (services.filter((s) => s === services[i]).length > 1 ? base : services[i]));
}

// containersSummary is the one line over an app's containers: how many run
// and what is wrong. "" when the app has none to report on.
export function containersSummary(status: number | undefined, containers: Pick<ContainerStatusWire, "status_code">[]): string {
  if (status === AppStatusCode.Stopped) return "Stopped — no containers running";
  if (containers.length === 0) return "No containers reported yet";
  const count = (...codes: number[]) => containers.filter((c) => codes.includes(c.status_code)).length;
  // Only an Active container runs: an Idle one was created but not started
  // (or is paused).
  const running = count(AppStatusCode.Active);
  const noun = (n: number) => (n === 1 ? "1 container" : `${n} containers`);
  const parts = [running === containers.length ? `${noun(running)} running` : `${running} of ${noun(containers.length)} running`];
  const idle = count(AppStatusCode.Idle);
  if (idle > 0) parts.push(`${idle} not started or paused`);
  const restarting = count(AppStatusCode.Restarting);
  const problems = count(AppStatusCode.Problematic);
  if (restarting > 0) parts.push(`${restarting} restarting`);
  if (problems > 0) parts.push(problems === 1 ? "1 with a problem" : `${problems} with problems`);
  return parts.join(" · ");
}
