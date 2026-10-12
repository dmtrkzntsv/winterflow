"use client"

import * as React from "react"

import { NavMain } from "@/components/nav-main"
import { NavApps } from "@/components/nav-apps"
import { NavSecondary } from "@/components/nav-secondary"
import { NavUser } from "@/components/nav-user"
import { ServerSwitcher } from "@/components/server-switcher"
import { navBottom, navMain, scopeNav } from "@/lib/nav"
import {
    Sidebar,
    SidebarContent,
    SidebarFooter,
    SidebarHeader,
    SidebarRail,
} from "@/components/ui/sidebar"
import { useServerPath } from "@/context/use-server-path"

// Platform navigation (lib/nav): Dashboard and the live Apps group on top,
// Monitoring + Server Settings pinned to the bottom, all pointing into the
// active server's scope.
export function AppSidebar({
    onAddServer,
    ...props
}: React.ComponentProps<typeof Sidebar> & { onAddServer?: () => void }) {
    const toServer = useServerPath()
    const main = React.useMemo(() => scopeNav(navMain, toServer), [toServer])
    const bottom = React.useMemo(() => scopeNav(navBottom, toServer), [toServer])
    return (
        <Sidebar collapsible="icon" {...props}>
            <SidebarHeader>
                <ServerSwitcher onAddServer={onAddServer} />
            </SidebarHeader>
            <SidebarContent>
                <NavMain items={main} />
                <NavApps />
                <NavSecondary items={bottom} className="mt-auto" />
            </SidebarContent>
            <SidebarFooter>
                <NavUser />
            </SidebarFooter>
            <SidebarRail />
        </Sidebar>
    )
}
