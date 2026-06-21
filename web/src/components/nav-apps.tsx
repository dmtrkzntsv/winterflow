"use client";

import {
  SidebarGroup,
  SidebarGroupLabel,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
} from "@/components/ui/sidebar";
import { AppIcon } from "@/components/app-icon";
import { useApps } from "@/context/use-apps";

// NavApps lists the active server's apps in the sidebar (v1 parity).
export function NavApps() {
  const { apps, loading } = useApps();

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
              <SidebarMenuButton>
                <AppIcon
                  name={app.name}
                  color={app.color}
                  className="size-5 text-[10px]"
                />
                <span className="truncate">{app.name}</span>
              </SidebarMenuButton>
            </SidebarMenuItem>
          ))
        )}
      </SidebarMenu>
    </SidebarGroup>
  );
}
