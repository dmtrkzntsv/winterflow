import { useLocation } from "react-router-dom";
import { Plus } from "lucide-react";

import { SidebarLink } from "@/components/sidebar-link";
import { StatusDot } from "@/components/status";
import { SidebarMenuSub, SidebarMenuSubButton, SidebarMenuSubItem } from "@/components/ui/sidebar";
import { useApps } from "@/context/use-apps";
import { useServers } from "@/context/use-servers";
import { appLabel } from "@/lib/kpi";
import { appPath, newAppPath } from "@/lib/routes";
import { appStatus } from "@/lib/status";
import { cn } from "@/lib/utils";

// NavApps is the live app list under Apps: each app of the active server
// with its status dot, then New app. Hidden when the sidebar is collapsed
// to icons.
export function NavApps() {
  const { apps, statusByApp, loading } = useApps();
  const { activeServerId } = useServers();
  const { pathname } = useLocation();
  if (!activeServerId) return null;
  const newApp = newAppPath(activeServerId);
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
        const status = appStatus(statusByApp[app.id]);
        return (
          <SidebarMenuSubItem key={app.id}>
            <SidebarMenuSubButton asChild isActive={pathname === to} className="max-lg:h-10">
              <SidebarLink to={to}>
                <StatusDot tone={status.tone} pulse={status.pulse} />
                {/* Not reconciled yet: the id prefix stands in for the name. */}
                <span className={cn("truncate", !app.name?.trim() && "font-mono text-muted-foreground")}>
                  {appLabel(app.name)}
                  <span className="sr-only">, {status.label}</span>
                </span>
              </SidebarLink>
            </SidebarMenuSubButton>
          </SidebarMenuSubItem>
        );
      })}
      <SidebarMenuSubItem>
        <SidebarMenuSubButton asChild isActive={pathname === newApp} className="text-muted-foreground max-lg:h-10">
          <SidebarLink to={newApp}>
            <Plus />
            <span>New app</span>
          </SidebarLink>
        </SidebarMenuSubButton>
      </SidebarMenuSubItem>
    </SidebarMenuSub>
  );
}
