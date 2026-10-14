import { Activity, Inbox, LayoutGrid, LayoutTemplate, Settings, type LucideIcon } from "lucide-react";

import { INBOX_PATH, SETTINGS_PATH, TEMPLATES_PATH, appsPath, monitoringPath } from "@/lib/routes";
import { serverPath } from "@/lib/server-paths";

export type NavItem = {
  title: string;
  url: string;
  icon: LucideIcon;
  isActive: (pathname: string) => boolean;
  // badge names the count shown beside the entry.
  badge?: "inbox";
};

// SidebarNav is the sidebar (spec §2): Apps (with the live app list under
// it), the pages, and Settings pinned above the account footer.
export type SidebarNav = { apps: NavItem; pages: NavItem[]; settings: NavItem };

// sidebarNav builds the sidebar for the active server: Apps and Monitoring
// are the server's, Inbox, Templates and Settings the organization's
// (Settings also lights on the server's own settings pages).
export function sidebarNav(serverId: string | null): SidebarNav {
  const apps = serverId ? appsPath(serverId) : "/";
  const pages: NavItem[] = [];
  if (serverId) {
    const monitoring = monitoringPath(serverId);
    pages.push({ title: "Monitoring", url: monitoring, icon: Activity, isActive: (p) => isNavActive(p, monitoring) });
  }
  pages.push(
    { title: "Inbox", url: INBOX_PATH, icon: Inbox, badge: "inbox", isActive: (p) => isNavActive(p, INBOX_PATH) },
    { title: "Templates", url: TEMPLATES_PATH, icon: LayoutTemplate, isActive: (p) => isNavActive(p, TEMPLATES_PATH) },
  );
  const serverSettings = serverId ? serverPath(serverId, "/settings") : null;
  return {
    apps: { title: "Apps", url: apps, icon: LayoutGrid, isActive: (p) => p === apps },
    pages,
    settings: {
      title: "Settings",
      url: SETTINGS_PATH,
      icon: Settings,
      isActive: (p) => isNavActive(p, SETTINGS_PATH) || (serverSettings !== null && isNavActive(p, serverSettings)),
    },
  };
}

// isNavActive reports whether a sidebar link matches the current path: an
// exact entry only on itself, any other entry on itself or a subpath.
export function isNavActive(pathname: string, url: string, exact = false): boolean {
  if (exact) return pathname === url;
  return pathname === url || pathname.startsWith(`${url}/`);
}

// The active sidebar entry is a pill (the sidebar accent) with its icon lit
// in the sidebar's sky blue, as in twillingate's shell.
export const navItemClass = "data-[active=true]:[&>svg]:text-sidebar-primary";
