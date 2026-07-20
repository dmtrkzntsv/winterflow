import { Fragment, useState } from "react"
import { Navigate, Outlet, useLocation } from "react-router-dom"

import { useProfile } from "@/context/use-profile"

import { AppSidebar } from "@/components/app-sidebar"
import { ServerSelectionDialog } from "@/components/server-creation-dialog"
import { isStandalone } from "@/config"
import { useServers } from "@/context/use-servers"
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
  const { profile } = useProfile()
  const location = useLocation()
  const { refresh } = useServers()
  const [isServerDialogOpen, setIsServerDialogOpen] = useState(false)

  // Users on a temporary password are forced to set their own before doing
  // anything else.
  if (
    profile?.must_change_password &&
    location.pathname !== "/user/password"
  ) {
    return <Navigate to="/user/password" replace />
  }

  // Adding servers is a cloud/distributed capability; the standalone build
  // ships a single embedded server, so the trigger is omitted there.
  const handleAddServer = isStandalone
    ? undefined
    : () => setIsServerDialogOpen(true)

  return (
    <SidebarProvider>
      <AppSidebar onAddServer={handleAddServer} />
      <AppLayoutContent />
      {!isStandalone ? (
        <ServerSelectionDialog
          isOpen={isServerDialogOpen}
          onClose={() => setIsServerDialogOpen(false)}
          onServerAdded={() => void refresh()}
        />
      ) : null}
    </SidebarProvider>
  )
}
