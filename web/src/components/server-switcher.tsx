"use client";

import { Check, ChevronsUpDown, Plus } from "lucide-react";

import { LogoSpinner } from "@/components/app-logo";
import { StatusDot } from "@/components/status";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuShortcut,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { SidebarMenu, SidebarMenuButton, SidebarMenuItem } from "@/components/ui/sidebar";
import { useSidebar } from "@/components/ui/use-sidebar";
import { isStandalone } from "@/config";
import type { Server } from "@/context/servers-context-base";
import { useServers } from "@/context/use-servers";
import { shortcutLabel } from "@/lib/keyboard";
import { serverStatus } from "@/lib/status";

const LOGO_BOX =
  "bg-white text-sidebar-primary-soft flex aspect-square size-8 items-center justify-center rounded-lg border border-sidebar-border/50";

function Logo() {
  return <LogoSpinner size="md" containerClassName={LOGO_BOX} iconClassName="text-sidebar-primary-soft" />;
}

// placeholderRow keeps the header stable (icon + two lines) while servers
// load or before the first one exists.
function placeholderRow(title: string, subtitle: string) {
  return (
    <SidebarMenu>
      <SidebarMenuItem>
        <SidebarMenuButton size="lg">
          <Logo />
          <div className="grid flex-1 text-left text-sm leading-tight">
            <span className="truncate font-medium">{title}</span>
            <span className="truncate text-xs text-muted-foreground">
              {subtitle}
            </span>
          </div>
        </SidebarMenuButton>
      </SidebarMenuItem>
    </SidebarMenu>
  );
}

// ServerSwitcher heads the sidebar (spec §2). Distributed: the active
// server with its status, a menu of every server (status dot, ⌘1…⌘9) and
// Add server. Standalone: the one embedded server's name, no menu.
export function ServerSwitcher({ onAddServer }: { onAddServer?: () => void }) {
  const { isMobile, setOpenMobile } = useSidebar();
  const { servers, activeServer, selectServer, statusByServer, loading } = useServers();

  if (!activeServer) {
    // Never collapse to nothing while loading or before the first server.
    if (loading) return placeholderRow("Winterflow", "Loading…");
    if (isStandalone) return placeholderRow("Winterflow", "This server");
    return (
      <SidebarMenu>
        <SidebarMenuItem>
          <SidebarMenuButton size="lg" onClick={() => onAddServer?.()} className="cursor-pointer">
            <div className="flex aspect-square size-8 items-center justify-center rounded-lg border border-sidebar-border bg-sidebar-accent">
              <Plus className="size-4" />
            </div>
            <div className="grid flex-1 text-left text-sm leading-tight">
              <span className="truncate font-medium">Add a server</span>
              <span className="truncate text-xs text-muted-foreground">No servers yet</span>
            </div>
          </SidebarMenuButton>
        </SidebarMenuItem>
      </SidebarMenu>
    );
  }

  // Standalone: one embedded server, nothing to switch or add. Not a button.
  if (isStandalone) {
    return (
      <SidebarMenu>
        <SidebarMenuItem>
          <div className="flex h-12 items-center gap-2 rounded-md p-2 text-sm group-data-[collapsible=icon]:size-8 group-data-[collapsible=icon]:p-0">
            <Logo />
            <span className="truncate font-medium group-data-[collapsible=icon]:hidden">{activeServer.name}</span>
          </div>
        </SidebarMenuItem>
      </SidebarMenu>
    );
  }

  const statusOf = (s: Server) =>
    serverStatus({ liveness: statusByServer?.[s.id], lastSeenAt: s.lastSeenAt, standalone: false });
  const current = statusOf(activeServer);
  const pick = (id: string) => {
    selectServer(id);
    if (isMobile) setOpenMobile(false);
  };

  return (
    <SidebarMenu>
      <SidebarMenuItem>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <SidebarMenuButton
              size="lg"
              className="data-[state=open]:bg-sidebar-accent data-[state=open]:text-sidebar-accent-foreground"
            >
              <Logo />
              <div className="grid flex-1 text-left text-sm leading-tight">
                <span className="truncate font-medium">{activeServer.name}</span>
                <span className="flex items-center gap-1.5 truncate text-xs text-muted-foreground">
                  <StatusDot tone={current.tone} pulse={current.pulse} />
                  {current.label}
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
            <DropdownMenuLabel className="text-xs text-muted-foreground">Servers</DropdownMenuLabel>
            {servers.map((s, index) => {
              const status = statusOf(s);
              return (
                <DropdownMenuItem key={s.id} onClick={() => pick(s.id)} className="min-h-10 gap-2 p-2">
                  <StatusDot tone={status.tone} pulse={status.pulse} label={status.label} />
                  <span className="truncate">{s.name}</span>
                  {s.id === activeServer.id ? <Check role="img" className="size-4" aria-label="Current server" /> : null}
                  {index < 9 ? <DropdownMenuShortcut>{shortcutLabel(index + 1)}</DropdownMenuShortcut> : null}
                </DropdownMenuItem>
              );
            })}
            <DropdownMenuSeparator />
            <DropdownMenuItem className="min-h-10 gap-2 p-2" onClick={() => onAddServer?.()}>
              <div className="flex size-6 items-center justify-center rounded-md border bg-transparent">
                <Plus className="size-4" />
              </div>
              <div className="font-medium text-muted-foreground">Add server</div>
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </SidebarMenuItem>
    </SidebarMenu>
  );
}
