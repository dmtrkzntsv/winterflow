"use client"

import * as React from "react"
import { type LucideIcon } from "lucide-react"
import { Link, useLocation } from "react-router-dom"

import {
    SidebarGroup,
    SidebarGroupContent,
    SidebarMenu,
    SidebarMenuButton,
    SidebarMenuItem,
} from "@/components/ui/sidebar"
import { isNavActive, navItemClass } from "@/lib/nav"

// NavSecondary renders bottom-pinned navigation links (e.g. Server Settings).
export function NavSecondary({
    items,
    ...props
}: {
    items: {
        title: string
        url: string
        icon: LucideIcon
    }[]
} & React.ComponentPropsWithoutRef<typeof SidebarGroup>) {
    const { pathname } = useLocation()
    return (
        <SidebarGroup {...props}>
            <SidebarGroupContent>
                <SidebarMenu>
                    {items.map((item) => (
                        <SidebarMenuItem key={item.title}>
                            <SidebarMenuButton
                                asChild
                                size="sm"
                                tooltip={item.title}
                                isActive={isNavActive(pathname, item.url)}
                                className={navItemClass}
                            >
                                <Link to={item.url}>
                                    <item.icon />
                                    <span>{item.title}</span>
                                </Link>
                            </SidebarMenuButton>
                        </SidebarMenuItem>
                    ))}
                </SidebarMenu>
            </SidebarGroupContent>
        </SidebarGroup>
    )
}
