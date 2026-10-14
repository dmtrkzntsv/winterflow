import { Fragment, useState } from "react"
import { Navigate, Outlet, useLocation } from "react-router-dom"

import { useProfile } from "@/context/use-profile"

import { AlertToasts } from "@/components/alerts/alert-toasts"
import { AppSidebar } from "@/components/app-sidebar"
import { ServerSelectionDialog } from "@/components/server-creation-dialog"
import { isStandalone } from "@/config"
import { useServers } from "@/context/use-servers"
import { settingsPath } from "@/lib/routes"
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
    // The sky wash sits behind the page title; the top bar is translucent
    // and blurred so content scrolls under it (twillingate's shell).
    <SidebarInset
      className="sky-wash min-w-0"
      style={{
        transition: 'margin-left 200ms ease-linear',
      }}
    >
        <header className="sticky top-0 z-20 flex h-12 shrink-0 items-center gap-2 border-b border-border/70 bg-background/55 backdrop-blur-md">
          <div className="flex min-w-0 items-center gap-2 px-3 sm:px-4">
            <SidebarTrigger className="-ml-1" />
            <Separator
              orientation="vertical"
              className="mr-1 data-[orientation=vertical]:h-4"
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
        <div className="flex flex-1 flex-col gap-4 p-4 sm:p-6">
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
    location.pathname !== settingsPath("password")
  ) {
    return <Navigate to={settingsPath("password")} replace />
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
      <AlertToasts />
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
