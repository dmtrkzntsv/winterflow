import { Activity, LayoutDashboard, Settings2 } from "lucide-react";

// Sidebar navigation: Dashboard (the apps page) on top, then the live Apps
// group (NavApps), and a bottom group — Monitoring of the switcher's active
// server and Server Settings — just above the account footer.
export const navMain = [
  {
    title: "Dashboard",
    url: "/",
    icon: LayoutDashboard,
    isActive: true,
  },
];

export const navBottom = [
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

// isNavActive reports whether a sidebar link matches the current path: the
// Dashboard ("/") only on itself, any other entry on itself or a subpath.
export function isNavActive(pathname: string, url: string): boolean {
  if (url === "/") return pathname === "/";
  return pathname === url || pathname.startsWith(`${url}/`);
}

// The active sidebar entry is a pill (the sidebar accent) with its icon lit
// in the sidebar's sky blue, as in twillingate's shell.
export const navItemClass = "data-[active=true]:[&>svg]:text-sidebar-primary";
