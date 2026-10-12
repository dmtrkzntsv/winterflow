import { Activity, LayoutDashboard } from "lucide-react";

// Platform navigation: Dashboard, the active server's Monitoring, then the
// live Apps group (NavApps); Server Settings is pinned to the bottom of the
// sidebar (app-sidebar.tsx) — mirroring v1's primary/secondary split.
export const navMain = [
  {
    title: "Dashboard",
    url: "/",
    icon: LayoutDashboard,
    isActive: true,
  },
  {
    // Charts of the server switcher's active server.
    title: "Monitoring",
    url: "/monitoring",
    icon: Activity,
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
