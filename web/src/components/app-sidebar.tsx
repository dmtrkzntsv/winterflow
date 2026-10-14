import { useMemo, type ReactNode } from "react";
import { useLocation } from "react-router-dom";

import { NavApps } from "@/components/nav-apps";
import { NavUser } from "@/components/nav-user";
import { ServerSwitcher } from "@/components/server-switcher";
import { SidebarLink } from "@/components/sidebar-link";
import { SidebarSearch } from "@/components/sidebar-search";
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuBadge,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarRail,
} from "@/components/ui/sidebar";
import { useServers } from "@/context/use-servers";
import { useInboxUnread } from "@/hooks/use-inbox-unread";
import { unreadBadge } from "@/lib/inbox";
import { navItemClass, sidebarNav, type NavItem } from "@/lib/nav";
import { cn } from "@/lib/utils";

// AppSidebar is the shell's navigation (spec §2): the server switcher and
// search on top; Apps with the live app list, Monitoring, Inbox (unread
// badge), Templates; Settings above the account footer. Collapsible to
// icons on a desktop, a sheet below 1024px.
export function AppSidebar({ onAddServer }: { onAddServer?: () => void }) {
  const { activeServerId } = useServers();
  const { pathname } = useLocation();
  const { count } = useInboxUnread();
  const nav = useMemo(() => sidebarNav(activeServerId), [activeServerId]);

  const entry = (item: NavItem, extra?: ReactNode) => {
    const unread = item.badge === "inbox" ? count : 0;
    return (
      <SidebarMenuItem key={item.title}>
        <SidebarMenuButton
          asChild
          tooltip={item.title}
          isActive={item.isActive(pathname)}
          className={cn(navItemClass, "relative max-lg:h-10")}
        >
          <SidebarLink to={item.url}>
            <item.icon />
            {unread > 0 ? (
              <span
                data-testid="inbox-dot"
                aria-hidden
                className="absolute right-1 top-1 hidden size-2 rounded-full bg-sidebar-primary group-data-[collapsible=icon]:block"
              />
            ) : null}
            <span>
              {item.title}
              {unread > 0 ? <span className="sr-only">, {unread} unread</span> : null}
            </span>
          </SidebarLink>
        </SidebarMenuButton>
        {unread > 0 ? (
          <SidebarMenuBadge
            aria-hidden
            className="rounded-full bg-sidebar-primary text-sidebar-primary-foreground peer-hover/menu-button:text-sidebar-primary-foreground peer-data-[active=true]/menu-button:text-sidebar-primary-foreground max-lg:top-2.5"
          >
            {unreadBadge(unread)}
          </SidebarMenuBadge>
        ) : null}
        {extra}
      </SidebarMenuItem>
    );
  };

  return (
    <Sidebar collapsible="icon">
      <SidebarHeader>
        <ServerSwitcher onAddServer={onAddServer} />
        <SidebarSearch />
      </SidebarHeader>
      <SidebarContent>
        <SidebarGroup>
          <SidebarMenu>
            {entry(nav.apps, <NavApps />)}
            {nav.pages.map((item) => entry(item))}
          </SidebarMenu>
        </SidebarGroup>
        <SidebarGroup className="mt-auto border-t border-sidebar-border">
          <SidebarMenu>{entry(nav.settings)}</SidebarMenu>
        </SidebarGroup>
      </SidebarContent>
      <SidebarFooter>
        <NavUser />
      </SidebarFooter>
      <SidebarRail />
    </Sidebar>
  );
}
