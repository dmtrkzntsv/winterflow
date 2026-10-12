"use client";

import { Link, useLocation } from "react-router-dom";

import {
  SidebarGroup,
  SidebarGroupLabel,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
} from "@/components/ui/sidebar";
import { AppIcon } from "@/components/app-icon";
import { useApps } from "@/context/use-apps";
import { appLabel } from "@/lib/kpi";
import { cn } from "@/lib/utils";
import { useServerPath } from "@/context/use-server-path";

// NavApps lists the active server's apps in the sidebar (v1 parity).
export function NavApps() {
  const { apps, loading } = useApps();
  const { pathname } = useLocation();
  const toServer = useServerPath();

  return (
    <SidebarGroup className="group-data-[collapsible=icon]:hidden">
      <SidebarGroupLabel>Apps</SidebarGroupLabel>
      <SidebarMenu>
        {loading && apps.length === 0 ? (
          <SidebarMenuItem>
            <SidebarMenuButton disabled>
              <span className="text-muted-foreground">Loading…</span>
            </SidebarMenuButton>
          </SidebarMenuItem>
        ) : apps.length === 0 ? (
          <SidebarMenuItem>
            <SidebarMenuButton disabled>
              <span className="text-muted-foreground">No apps yet</span>
            </SidebarMenuButton>
          </SidebarMenuItem>
        ) : (
          apps.map((app) => (
            <SidebarMenuItem key={app.id}>
              <SidebarMenuButton
                asChild
                isActive={pathname === toServer(`/app/${app.id}`)}
              >
                <Link to={toServer(`/app/${app.id}`)}>
                  <AppIcon
                    name={app.name}
                    id={app.id}
                    icon={app.icon}
                    color={app.color}
                    className="size-5 text-[10px]"
                  />
                  {/* Not reconciled yet: the id prefix stands in for the name. */}
                  <span className={cn("truncate", !app.name?.trim() && "font-mono text-muted-foreground")}>
                    {appLabel(app.name, app.id)}
                  </span>
                </Link>
              </SidebarMenuButton>
            </SidebarMenuItem>
          ))
        )}
      </SidebarMenu>
    </SidebarGroup>
  );
}
