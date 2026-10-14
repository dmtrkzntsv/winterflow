import { appLabel } from "@/lib/kpi";
import { INBOX_PATH, TEMPLATES_PATH, appPath, appsPath, monitoringPath, newAppPath } from "@/lib/routes";
import { serverPath } from "@/lib/server-paths";
import type { SettingsSection } from "@/lib/settings-nav";

// The ⌘K palette's destinations (spec §2): the apps on this server, every
// page, every Settings section, the other servers, and New app. Contextual
// actions (an app page's Restart, …) come from the palette context, theme
// and Add server from the palette itself.

export type PaletteGroup = "Apps" | "Pages" | "Settings" | "Servers" | "Actions";

export type PaletteNavEntry = {
  // id is stable across sessions (recent items are stored by it).
  id: string;
  group: PaletteGroup;
  label: string;
  to: string;
  // hint tells same-named entries apart (the Settings section's title).
  hint?: string;
  keywords?: string[];
};

export type RecentEntry = { id: string; label: string; to: string };

export const RECENT_KEY = "wf.palette.recent";
export const RECENT_MAX = 5;

export function paletteNavEntries({
  serverId,
  apps,
  servers,
  settings,
  standalone,
}: {
  serverId: string | null;
  // apps may span servers; only the active server's are offered.
  apps: { id: string; name: string; serverId: string }[];
  servers: { id: string; name: string }[];
  settings: SettingsSection[];
  standalone: boolean;
}): PaletteNavEntry[] {
  const entries: PaletteNavEntry[] = [];
  if (serverId) {
    for (const app of apps) {
      if (app.serverId !== serverId) continue;
      entries.push({ id: `app:${app.id}`, group: "Apps", label: appLabel(app.name, app.id), to: appPath(serverId, app.id), keywords: ["app"] });
    }
    entries.push(
      { id: "page:apps", group: "Pages", label: "Apps", to: appsPath(serverId), keywords: ["home", "dashboard"] },
      { id: "page:monitoring", group: "Pages", label: "Monitoring", to: monitoringPath(serverId), keywords: ["metrics", "cpu", "memory", "disk"] },
    );
  }
  entries.push(
    { id: "page:inbox", group: "Pages", label: "Inbox", to: INBOX_PATH, keywords: ["alerts", "notifications"] },
    { id: "page:templates", group: "Pages", label: "Templates", to: TEMPLATES_PATH, keywords: ["stack"] },
  );
  for (const section of settings) {
    for (const item of section.items) {
      entries.push({
        id: `settings:${item.to}`,
        group: "Settings",
        label: item.label,
        to: item.to,
        hint: section.title,
        keywords: ["settings", section.title],
      });
    }
  }
  if (!standalone) {
    for (const server of servers) {
      if (server.id === serverId) continue;
      entries.push({ id: `server:${server.id}`, group: "Servers", label: server.name, to: serverPath(server.id), hint: "Switch server", keywords: ["server", "switch"] });
    }
  }
  if (serverId) {
    entries.push({ id: "action:new-app", group: "Actions", label: "New app", to: newAppPath(serverId), keywords: ["create", "add", "deploy"] });
  }
  return entries;
}

function isRecentEntry(value: unknown): value is RecentEntry {
  if (typeof value !== "object" || value === null) return false;
  const v = value as Record<string, unknown>;
  return typeof v.id === "string" && typeof v.label === "string" && typeof v.to === "string";
}

// readRecent is the stored recent items, newest first; anything unreadable
// reads as none.
export function readRecent(): RecentEntry[] {
  try {
    const raw = window.localStorage.getItem(RECENT_KEY);
    const list: unknown = raw ? JSON.parse(raw) : [];
    return Array.isArray(list) ? list.filter(isRecentEntry).slice(0, RECENT_MAX) : [];
  } catch {
    return [];
  }
}

export function pushRecent(entry: RecentEntry): void {
  const next = [entry, ...readRecent().filter((r) => r.id !== entry.id)].slice(0, RECENT_MAX);
  try {
    window.localStorage.setItem(RECENT_KEY, JSON.stringify(next));
  } catch {
    // Storage blocked: no recent items.
  }
}

// visibleRecent keeps the recent items that still exist (an app deleted
// or on another server is gone), as they are now (a renamed app).
export function visibleRecent(recent: RecentEntry[], entries: PaletteNavEntry[]): PaletteNavEntry[] {
  const byId = new Map(entries.map((e) => [e.id, e]));
  return recent.flatMap((r) => {
    const entry = byId.get(r.id);
    return entry ? [entry] : [];
  });
}
