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
  SidebarTrigger,
} from "@/components/ui/sidebar";
import { useSidebar } from "@/components/ui/use-sidebar";
import { useServers } from "@/context/use-servers";
import { useInboxUnread } from "@/hooks/use-inbox-unread";
import { unreadBadge } from "@/lib/inbox";
import { shortcutLabel } from "@/lib/keyboard";
import { navItemClass, sidebarNav, type NavItem } from "@/lib/nav";
import { cn } from "@/lib/utils";

// CollapseButton folds the sidebar to icons and back on a desktop (Ctrl/⌘+B
// does the same); the phone's sheet has its own menu button.
function CollapseButton() {
  const { state } = useSidebar();
  const label = state === "expanded" ? "Collapse sidebar" : "Expand sidebar";
  return (
    <SidebarTrigger
      aria-label={label}
      title={`${label} (${shortcutLabel("B")})`}
      className="size-8 shrink-0 text-sidebar-foreground/70 max-lg:hidden"
    />
  );
}

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
        <div className="flex items-center gap-1 group-data-[collapsible=icon]:flex-col">
          <div className="min-w-0 flex-1 group-data-[collapsible=icon]:flex-none">
            <NavUser />
          </div>
          <CollapseButton />
        </div>
      </SidebarFooter>
      <SidebarRail />
    </Sidebar>
  );
}
