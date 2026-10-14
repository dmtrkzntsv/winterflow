import { serverPath } from "@/lib/server-paths";

// The route table of the UX rebuild (spec §2 "Routes"): one builder per
// page so links never spell paths by hand, and the mapping of every
// pre-rebuild URL to its new place.

export type NewAppSource = "template" | "compose" | "git";
export type AppTab = "overview" | "logs" | "configuration" | "backups" | "metrics" | "settings";
export type ServerSettingsSection = "general" | "domains" | "storages" | "docker";
export type OrgSettingsSection = "organization" | "members" | "variables" | "notifications" | "password" | "tokens";

export const INBOX_PATH = "/inbox";
export const TEMPLATES_PATH = "/templates";
export const NEW_TEMPLATE_PATH = "/templates/new";
export const SETTINGS_PATH = "/settings";

export function appsPath(serverId: string): string {
  return serverPath(serverId);
}

export function newAppSub(from?: NewAppSource): string {
  return from ? `/new?from=${from}` : "/new";
}

export function newAppPath(serverId: string, from?: NewAppSource): string {
  return serverPath(serverId, newAppSub(from));
}

export function appSub(appId: string, tab?: AppTab): string {
  const base = `/app/${encodeURIComponent(appId)}`;
  return tab ? `${base}?tab=${tab}` : base;
}

export function appPath(serverId: string, appId: string, tab?: AppTab): string {
  return serverPath(serverId, appSub(appId, tab));
}

export function monitoringPath(serverId: string): string {
  return serverPath(serverId, "/monitoring");
}

export function serverSettingsSub(section: ServerSettingsSection = "general"): string {
  return `/settings/${section}`;
}

export function serverSettingsPath(serverId: string, section: ServerSettingsSection = "general"): string {
  return serverPath(serverId, serverSettingsSub(section));
}

export function settingsPath(section: OrgSettingsSection): string {
  return `${SETTINGS_PATH}/${section}`;
}

export function templatePath(templateId: string): string {
  return `${TEMPLATES_PATH}/${encodeURIComponent(templateId)}`;
}

// LEGACY_APP_TABS maps an app page ?tab= value of before the rebuild to
// its new name (the URL is rewritten in place).
export const LEGACY_APP_TABS = { editor: "configuration" } as const satisfies Record<string, AppTab>;

// legacyServerSettingsTarget is where /server/:id/settings?tab=… went:
// each tab is a page of Settings → Server now, the server's variables are
// part of Settings → Shared variables, and the agent update is on General.
export function legacyServerSettingsTarget(serverId: string, tab: string | null): string {
  switch (tab) {
    case "variables":
      return settingsPath("variables");
    case "domains":
      return serverSettingsPath(serverId, "domains");
    case "storages":
      return serverSettingsPath(serverId, "storages");
    case "registries":
    case "networks":
    case "volumes":
      return serverSettingsPath(serverId, "docker");
    default:
      return serverSettingsPath(serverId, "general");
  }
}

// legacyAlertsTarget is where /org/alerts?tab=… went: the history is the
// Inbox, rules and channels are Settings → Notifications.
export function legacyAlertsTarget(tab: string | null): string {
  return tab === "rules" || tab === "channels" ? settingsPath("notifications") : INBOX_PATH;
}

// withQuery appends an old URL's query to a redirect target that has none
// of its own, and its hash in any case.
export function withQuery(target: string, search: string, hash: string): string {
  const base = target.includes("?") || !search ? target : `${target}${search}`;
  return `${base}${hash}`;
}

// isWidePath reports the pages that use the full width (the app page with
// its logs and charts, Monitoring); every other page is capped at 1100px.
export function isWidePath(pathname: string): boolean {
  return /^\/server\/[^/]+\/(app\/[^/]+|monitoring)\/?$/.test(pathname);
}
