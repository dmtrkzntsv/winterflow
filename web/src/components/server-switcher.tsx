"use client";

import * as React from "react";
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

export function ServerSwitcher({
  servers,
}: {
  servers: {
    name: string;
    status: string;
  }[];
}) {
  const { isMobile } = useSidebar();
  const [activeServer, setActiveServer] = React.useState(servers[0]);

  if (!activeServer) {
    return null;
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
                <span className="truncate font-medium">{activeServer.name}</span>
                <span className="truncate text-xs">{activeServer.status}</span>
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
                key={server.name}
                onClick={() => setActiveServer(server)}
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
            <DropdownMenuItem className="gap-2 p-2">
              <div className="flex size-6 items-center justify-center rounded-md border bg-transparent">
                <Plus className="size-4" />
              </div>
              <div className="text-muted-foreground font-medium">Add server</div>
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </SidebarMenuItem>
    </SidebarMenu>
  );
}
