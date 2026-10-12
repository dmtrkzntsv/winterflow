import { Activity, LayoutDashboard, Settings2, type LucideIcon } from "lucide-react";

export type NavItem = {
  title: string;
  url: string;
  icon: LucideIcon;
  // exact entries light up only on their own URL, not on subpaths.
  exact?: boolean;
};

// Sidebar navigation: Dashboard (the apps page) on top, then the live Apps
// group (NavApps), and a bottom group — Monitoring of the switcher's active
// server and Server Settings — just above the account footer. URLs are
// relative to the active server's scope (/server/:serverId); scopeNav makes
// them absolute.
export const navMain: NavItem[] = [
  {
    title: "Dashboard",
    url: "",
    icon: LayoutDashboard,
    exact: true,
  },
];

export const navBottom: NavItem[] = [
  {
    title: "Monitoring",
    url: "/monitoring",
    icon: Activity,
  },
  {
    title: "Server Settings",
    url: "/settings",
    icon: Settings2,
  },
];

// scopeNav points a nav group into a server's scope with a path builder such
// as useServerPath's.
export function scopeNav(items: NavItem[], toServer: (sub?: string) => string): NavItem[] {
  return items.map((item) => ({ ...item, url: toServer(item.url) }));
}

// isNavActive reports whether a sidebar link matches the current path: an
// exact entry (the Dashboard) only on itself, any other entry on itself or a
// subpath.
export function isNavActive(pathname: string, url: string, exact = false): boolean {
  if (exact) return pathname === url;
  return pathname === url || pathname.startsWith(`${url}/`);
}

// The active sidebar entry is a pill (the sidebar accent) with its icon lit
// in the sidebar's sky blue, as in twillingate's shell.
export const navItemClass = "data-[active=true]:[&>svg]:text-sidebar-primary";
