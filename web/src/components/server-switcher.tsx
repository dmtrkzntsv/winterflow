"use client";

import { ChevronsUpDown, Plus } from "lucide-react";

import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuShortcut,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
} from "@/components/ui/sidebar";
import { useSidebar } from "@/components/ui/use-sidebar";
import { LogoSpinner } from "@/components/app-logo";
import { isStandalone } from "@/config";
import { useServers } from "@/context/use-servers";
import type { Server } from "@/context/servers-context-base";

// A server is considered online if it has reported in within this window.
const ONLINE_WINDOW_MS = 2 * 60 * 1000;

function statusLabel(server: Server): string {
  if (!server.lastSeenAt) {
    return "Never connected";
  }
  const seen = new Date(server.lastSeenAt).getTime();
  if (Number.isNaN(seen)) {
    return "Unknown";
  }
  return Date.now() - seen <= ONLINE_WINDOW_MS ? "Online" : "Offline";
}

export function ServerSwitcher({
  onAddServer,
}: {
  onAddServer?: () => void;
}) {
  const { isMobile } = useSidebar();
  const { servers, activeServer, setActiveServerId } = useServers();

  if (!activeServer) {
    return null;
  }

  // Standalone: a single embedded server, no switching and no "add server".
  if (isStandalone) {
    return (
      <SidebarMenu>
        <SidebarMenuItem>
          <SidebarMenuButton size="lg">
            <LogoSpinner
              size="md"
              containerClassName="bg-white text-sidebar-primary-soft flex aspect-square size-8 items-center justify-center rounded-lg border border-sidebar-border/50"
              iconClassName="text-sidebar-primary-soft"
            />
            <div className="grid flex-1 text-left text-sm leading-tight">
              <span className="truncate font-medium">{activeServer.name}</span>
              <span className="truncate text-xs">
                {statusLabel(activeServer)}
              </span>
            </div>
          </SidebarMenuButton>
        </SidebarMenuItem>
      </SidebarMenu>
    );
  }

  return (
    <SidebarMenu>
      <SidebarMenuItem>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <SidebarMenuButton
              size="lg"
              className="data-[state=open]:bg-sidebar-accent data-[state=open]:text-sidebar-accent-foreground"
            >
              <LogoSpinner
                size="md"
                containerClassName="bg-white text-sidebar-primary-soft flex aspect-square size-8 items-center justify-center rounded-lg border border-sidebar-border/50"
                iconClassName="text-sidebar-primary-soft"
              />
              <div className="grid flex-1 text-left text-sm leading-tight">
                <span className="truncate font-medium">
                  {activeServer.name}
                </span>
                <span className="truncate text-xs">
                  {statusLabel(activeServer)}
                </span>
              </div>
              <ChevronsUpDown className="ml-auto" />
            </SidebarMenuButton>
          </DropdownMenuTrigger>
          <DropdownMenuContent
            className="w-(--radix-dropdown-menu-trigger-width) min-w-56 rounded-lg"
            align="start"
            side={isMobile ? "bottom" : "right"}
            sideOffset={4}
          >
            <DropdownMenuLabel className="text-muted-foreground text-xs">
              Servers
            </DropdownMenuLabel>
            {servers.map((server, index) => (
              <DropdownMenuItem
                key={server.id}
                onClick={() => setActiveServerId(server.id)}
                className="gap-2 p-2"
              >
                <LogoSpinner
                  size="sm"
                  containerClassName="bg-white text-sidebar-primary-soft flex size-6 items-center justify-center rounded-md border border-sidebar-border/50"
                  iconClassName="text-sidebar-primary-soft shrink-0"
                />
                {server.name}
                <DropdownMenuShortcut>⌘{index + 1}</DropdownMenuShortcut>
              </DropdownMenuItem>
            ))}
            <DropdownMenuSeparator />
            <DropdownMenuItem
              className="gap-2 p-2"
              onClick={() => onAddServer?.()}
            >
              <div className="flex size-6 items-center justify-center rounded-md border bg-transparent">
                <Plus className="size-4" />
              </div>
              <div className="text-muted-foreground font-medium">
                Add server
              </div>
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </SidebarMenuItem>
    </SidebarMenu>
  );
}
