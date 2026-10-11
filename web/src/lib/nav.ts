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
