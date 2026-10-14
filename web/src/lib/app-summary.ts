import { AppStatusCode, type App, type AppDomain } from "@/context/apps-context-base";
import { hasRunningContainers, type ContainerStatusWire } from "@/lib/app-status";
import type { BackupHealth } from "@/lib/backups";
import type { AppTab } from "@/lib/routes";

// What the Apps page and the app page say about an app at a glance: where
// it lives, how many apps run, the one warning a card carries.

// AppAddress is a public address of an app: the hostname and the URL that
// opens it (https when the route has TLS).
export type AppAddress = { host: string; url: string; https: boolean };

function addressOf(d: AppDomain): AppAddress {
  return { host: d.domain, url: `${d.ssl ? "https" : "http"}://${d.domain}`, https: d.ssl };
}

// appAddresses are the app's routes in the order the API lists them; a
// redirect is not where the app lives.
export function appAddresses(app: Pick<App, "domains">): AppAddress[] {
  return (app.domains ?? []).filter((d) => d.kind === "route").map(addressOf);
}

// primaryAddress is the address the UI opens ("Open ↗"), or null.
export function primaryAddress(app: Pick<App, "domains">): AppAddress | null {
  return appAddresses(app)[0] ?? null;
}

export type AppCounts = { running: number; problem: number; stopped: number };

// countApps sorts the apps by their live status: running (active, idle,
// restarting), with a problem, stopped. An app without a known status is
// in none (the UI never infers "down" from missing data).
export function countApps(apps: Pick<App, "id">[], statusByApp: Record<string, number>): AppCounts {
  const counts: AppCounts = { running: 0, problem: 0, stopped: 0 };
  for (const a of apps) {
    const code = statusByApp[a.id];
    if (code === AppStatusCode.Active || code === AppStatusCode.Idle || code === AppStatusCode.Restarting) counts.running++;
    else if (code === AppStatusCode.Problematic) counts.problem++;
    else if (code === AppStatusCode.Stopped) counts.stopped++;
  }
  return counts;
}

// appsSubtitle is the Apps page's subtitle: "homelab · 3 running · 1
// stopped" (zero counts other than running are left out).
export function appsSubtitle(serverName: string, total: number, counts: AppCounts): string {
  const parts = [serverName];
  if (total === 0) {
    parts.push("no apps yet");
  } else {
    parts.push(`${counts.running} running`);
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
  const running = count(AppStatusCode.Active, AppStatusCode.Idle);
  const noun = (n: number) => (n === 1 ? "1 container" : `${n} containers`);
  const parts = [running === containers.length ? `${noun(running)} running` : `${running} of ${noun(containers.length)} running`];
  const restarting = count(AppStatusCode.Restarting);
  const problems = count(AppStatusCode.Problematic);
  if (restarting > 0) parts.push(`${restarting} restarting`);
  if (problems > 0) parts.push(problems === 1 ? "1 with a problem" : `${problems} with problems`);
  return parts.join(" · ");
}
