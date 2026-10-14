import { useLocation } from "react-router-dom";
import { Plus } from "lucide-react";

import { useNewAppOpener } from "@/components/new-app/new-app-opener";
import { SidebarLink } from "@/components/sidebar-link";
import { StatusDot } from "@/components/status";
import { SidebarMenuSub, SidebarMenuSubButton, SidebarMenuSubItem } from "@/components/ui/sidebar";
import { useSidebar } from "@/components/ui/use-sidebar";
import { useApps } from "@/context/use-apps";
import { useServers } from "@/context/use-servers";
import { appLabel } from "@/lib/kpi";
import { appPath } from "@/lib/routes";
import { appStatus } from "@/lib/status";
import { cn } from "@/lib/utils";

// NavApps is the live app list under Apps: each app of the active server
// with its status dot, then New app, which opens the shell's chooser (the
// same as the Apps page's button; it lives above the sidebar, so closing
// the phone sheet first leaves it open). Hidden when the sidebar is
// collapsed to icons.
export function NavApps() {
  const { apps, statusByApp, deployFailures, loading } = useApps();
  const { activeServerId } = useServers();
  const { pathname } = useLocation();
  const { isMobile, setOpenMobile } = useSidebar();
  const openNewApp = useNewAppOpener();
  if (!activeServerId) return null;
  const serverApps = apps.filter((app) => app.serverId === activeServerId);

  return (
    <SidebarMenuSub>
      {loading && serverApps.length === 0 ? (
        <SidebarMenuSubItem>
          <span className="px-2 text-xs text-muted-foreground">Loading…</span>
        </SidebarMenuSubItem>
      ) : null}
      {serverApps.map((app) => {
        const to = appPath(activeServerId, app.id);
        const status = appStatus(statusByApp[app.id], Boolean(deployFailures?.[app.id]));
        return (
          <SidebarMenuSubItem key={app.id}>
            <SidebarMenuSubButton asChild isActive={pathname === to} className="max-lg:h-10">
              <SidebarLink to={to}>
                <StatusDot tone={status.tone} pulse={status.pulse} />
                <span className={cn("truncate", !app.name?.trim() && "text-muted-foreground")}>
                  {appLabel(app.name)}
                  <span className="sr-only">, {status.label}</span>
                </span>
              </SidebarLink>
            </SidebarMenuSubButton>
          </SidebarMenuSubItem>
        );
      })}
      {openNewApp ? (
        <SidebarMenuSubItem>
          <SidebarMenuSubButton asChild className="text-muted-foreground max-lg:h-10">
            <button
              type="button"
              className="w-full"
              onClick={() => {
                if (isMobile) setOpenMobile(false);
                openNewApp();
              }}
            >
              <Plus />
              <span>New app</span>
            </button>
          </SidebarMenuSubButton>
        </SidebarMenuSubItem>
      ) : null}
    </SidebarMenuSub>
  );
}
