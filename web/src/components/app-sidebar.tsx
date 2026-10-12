"use client"

import * as React from "react"

import { NavMain } from "@/components/nav-main"
import { NavApps } from "@/components/nav-apps"
import { NavSecondary } from "@/components/nav-secondary"
import { NavUser } from "@/components/nav-user"
import { ServerSwitcher } from "@/components/server-switcher"
import { navBottom, navMain } from "@/lib/nav"
import {
    Sidebar,
    SidebarContent,
    SidebarFooter,
    SidebarHeader,
    SidebarRail,
} from "@/components/ui/sidebar"

export function AppSidebar({
    onAddServer,
    ...props
}: React.ComponentProps<typeof Sidebar> & { onAddServer?: () => void }) {
    return (
        <Sidebar collapsible="icon" {...props}>
            <SidebarHeader>
                <ServerSwitcher onAddServer={onAddServer} />
            </SidebarHeader>
            <SidebarContent>
                <NavMain items={navMain} />
                <NavApps />
                <NavSecondary items={navBottom} className="mt-auto" />
            </SidebarContent>
            <SidebarFooter>
                <NavUser />
            </SidebarFooter>
            <SidebarRail />
        </Sidebar>
    )
}
