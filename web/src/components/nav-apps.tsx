import { useLocation } from "react-router-dom";
import { Plus } from "lucide-react";

import { useNewAppDialog } from "@/components/new-app/use-new-app-dialog";
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
// with its status dot, then New app, which opens the same chooser as the
// Apps page's button (closing the phone sheet first). Hidden when the
// sidebar is collapsed to icons.
export function NavApps() {
  const { apps, statusByApp, deployFailures, loading } = useApps();
  const { activeServerId } = useServers();
  const { pathname } = useLocation();
  const { isMobile, setOpenMobile } = useSidebar();
  const chooser = useNewAppDialog(activeServerId ?? "");
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
      <SidebarMenuSubItem>
        <SidebarMenuSubButton asChild className="text-muted-foreground max-lg:h-10">
          <button
            type="button"
            className="w-full"
            onClick={() => {
              if (isMobile) setOpenMobile(false);
              chooser.open();
            }}
          >
            <Plus />
            <span>New app</span>
          </button>
        </SidebarMenuSubButton>
        {chooser.dialog}
      </SidebarMenuSubItem>
    </SidebarMenuSub>
  );
}
