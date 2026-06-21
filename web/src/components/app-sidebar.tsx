"use client"

import * as React from "react"
import { Container, LayoutDashboard } from "lucide-react"

import { NavMain } from "@/components/nav-main"
import { NavApps } from "@/components/nav-apps"
import { NavUser } from "@/components/nav-user"
import { ServerSwitcher } from "@/components/server-switcher"
import {
    Sidebar,
    SidebarContent,
    SidebarFooter,
    SidebarHeader,
    SidebarRail,
} from "@/components/ui/sidebar"

// Platform navigation. Mirrors v1 (Dashboard + an Apps group); catalog,
// settings, and organization management are out of scope for v2 and are
// intentionally omitted rather than stubbed.
const navMain = [
    {
        title: "Dashboard",
        url: "/",
        icon: LayoutDashboard,
        isActive: true,
    },
    {
        title: "Docker",
        url: "/docker",
        icon: Container,
    },
]

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
            </SidebarContent>
            <SidebarFooter>
                <NavUser />
            </SidebarFooter>
            <SidebarRail />
        </Sidebar>
    )
}
