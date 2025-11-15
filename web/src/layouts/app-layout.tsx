import { Fragment, useState } from "react"
import { Outlet } from "react-router-dom"

import { AppSidebar } from "@/components/app-sidebar"
import {
  Breadcrumb,
  BreadcrumbItem,
  BreadcrumbLink,
  BreadcrumbList,
  BreadcrumbPage,
  BreadcrumbSeparator,
} from "@/components/ui/breadcrumb"
import { Separator } from "@/components/ui/separator"
import {
  SidebarInset,
  SidebarProvider,
  SidebarTrigger,
} from "@/components/ui/sidebar"

export type BreadcrumbEntry = {
  label: string
  href?: string
  hideOnMobile?: boolean
}

export type AppLayoutOutletContext = {
  setBreadcrumbs: (items: BreadcrumbEntry[]) => void
}

function AppLayoutContent() {
  const [breadcrumbs, setBreadcrumbs] = useState<BreadcrumbEntry[]>([])

  return (
    <SidebarInset
      style={{
        transition: 'margin-left 200ms ease-linear',
      }}
    >
        <header className="flex h-16 shrink-0 items-center gap-2 transition-[width,height] ease-linear group-has-data-[collapsible=icon]/sidebar-wrapper:h-12">
          <div className="flex items-center gap-2 px-4">
            <SidebarTrigger className="-ml-1" />
            <Separator
              orientation="vertical"
              className="mr-2 data-[orientation=vertical]:h-4"
            />
            {breadcrumbs.length > 0 ? (
              <Breadcrumb>
                <BreadcrumbList>
                  {breadcrumbs.map((crumb, index) => {
                    const isLast = index === breadcrumbs.length - 1
                    const itemClass = crumb.hideOnMobile ? "hidden md:block" : undefined
                    const separatorClass = crumb.hideOnMobile
                      ? "hidden md:block"
                      : undefined

                    return (
                      <Fragment key={`${crumb.label}-${index}`}>
                        <BreadcrumbItem className={itemClass}>
                          {isLast ? (
                            <BreadcrumbPage>{crumb.label}</BreadcrumbPage>
                          ) : (
                            <BreadcrumbLink href={crumb.href ?? "#"}>
                              {crumb.label}
                            </BreadcrumbLink>
                          )}
                        </BreadcrumbItem>
                        {!isLast ? (
                          <BreadcrumbSeparator className={separatorClass} />
                        ) : null}
                      </Fragment>
                    )
                  })}
                </BreadcrumbList>
              </Breadcrumb>
            ) : null}
          </div>
        </header>
        <div className="flex flex-1 flex-col gap-4 p-4 pt-0">
          <Outlet context={{ setBreadcrumbs }} />
        </div>
      </SidebarInset>
  )
}

export function AppLayout() {
  return (
    <SidebarProvider>
      <AppSidebar />
      <AppLayoutContent />
    </SidebarProvider>
  )
}
