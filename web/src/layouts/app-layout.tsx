import { useState, type CSSProperties } from "react"
import { Navigate, Outlet, useLocation } from "react-router-dom"
import { Menu, Search } from "lucide-react"

import { AlertToasts } from "@/components/alerts/alert-toasts"
import { AppSidebar } from "@/components/app-sidebar"
import { CommandPalette } from "@/components/command-palette"
import { NewAppDialogProvider } from "@/components/new-app/new-app-dialog-provider"
import { ServerSelectionDialog } from "@/components/server-creation-dialog"
import { Button } from "@/components/ui/button"
import { SidebarInset, SidebarProvider } from "@/components/ui/sidebar"
import { useSidebar } from "@/components/ui/use-sidebar"
import { isStandalone } from "@/config"
import { InboxUnreadProvider } from "@/context/inbox-unread-context"
import { CommandPaletteProvider } from "@/context/palette-context"
import { useCommandPalette } from "@/context/use-palette"
import { useProfile } from "@/context/use-profile"
import { useServers } from "@/context/use-servers"
import { useIsMobile } from "@/hooks/use-mobile"
import { useServerShortcuts } from "@/hooks/use-server-shortcuts"
import { PageTitleProvider } from "@/layouts/page-title"
import type { ShellOutlet } from "@/layouts/shell-outlet"
import { useTopBarTitle } from "@/layouts/use-page-title"
import { isWidePath, settingsPath } from "@/lib/routes"
import { cn } from "@/lib/utils"

// The sidebar is 240px wide on a desktop (spec §2), collapsible to icons.
const SIDEBAR_STYLE = { "--sidebar-width": "15rem" } as CSSProperties

// MobileTopBar is the phone/tablet chrome (below 1024px): the menu button
// opens the sidebar sheet, the page names itself, search opens ⌘K.
function MobileTopBar() {
  const { openMobile, setOpenMobile } = useSidebar()
  const { openPalette } = useCommandPalette()
  const title = useTopBarTitle()
  return (
    <header
      data-testid="mobile-top-bar"
      className="sticky top-0 z-20 flex h-14 shrink-0 items-center gap-1 border-b border-border/70 bg-background/70 px-2 backdrop-blur-md"
    >
      <Button
        variant="ghost"
        size="icon"
        className="size-10"
        aria-label="Open menu"
        aria-haspopup="dialog"
        aria-expanded={openMobile}
        onClick={() => setOpenMobile(true)}
      >
        <Menu className="size-5" />
      </Button>
      <p className="min-w-0 flex-1 truncate text-center text-[15px] font-semibold">{title}</p>
      <Button variant="ghost" size="icon" className="size-10" aria-label="Search" onClick={openPalette}>
        <Search className="size-5" />
      </Button>
    </header>
  )
}

// MAIN_ID is the page area's id: the skip link's target.
const MAIN_ID = "main-content"

// SkipLink is the first Tab stop: it moves focus past the sidebar to the
// page (without touching the URL), shown only while focused.
function SkipLink() {
  return (
    <a
      href={`#${MAIN_ID}`}
      onClick={(e) => {
        e.preventDefault()
        document.getElementById(MAIN_ID)?.focus()
      }}
      className="sr-only rounded-md bg-background text-sm font-medium shadow-lg ring-2 ring-ring focus:not-sr-only focus:fixed focus:top-3 focus:left-3 focus:z-50 focus:px-3 focus:py-2"
    >
      Skip to content
    </a>
  )
}

// AppMain is the page area: the sky wash, no breadcrumb bar (every page
// starts with its own PageHeader), reading pages capped near 1100px.
function AppMain({ onAddServer }: ShellOutlet) {
  const isMobile = useIsMobile()
  const { pathname } = useLocation()
  return (
    <SidebarInset className="sky-wash min-w-0">
      {isMobile ? <MobileTopBar /> : null}
      <div
        id={MAIN_ID}
        tabIndex={-1}
        data-testid="page"
        className={cn(
          "mx-auto flex w-full flex-1 flex-col gap-4 p-4 outline-none sm:p-6 lg:p-8",
          !isWidePath(pathname) && "max-w-[1100px]",
        )}
      >
        <Outlet context={{ onAddServer } satisfies ShellOutlet} />
      </div>
    </SidebarInset>
  )
}

export function AppLayout() {
  const { profile } = useProfile()
  const location = useLocation()
  const { refresh } = useServers()
  const [isServerDialogOpen, setIsServerDialogOpen] = useState(false)
  useServerShortcuts(!isStandalone)

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

  // One unread-Inbox state for the shell: the sidebar badge and the Inbox
  // page read the same instance (one read per alert, not one per consumer).
  // The New app chooser lives here too, above the sidebar's phone sheet.
  return (
    <PageTitleProvider>
      <InboxUnreadProvider>
        <CommandPaletteProvider>
          <SidebarProvider style={SIDEBAR_STYLE}>
            <NewAppDialogProvider>
              <SkipLink />
              <AppSidebar onAddServer={handleAddServer} />
              <AppMain onAddServer={handleAddServer} />
              <CommandPalette onAddServer={handleAddServer} />
              <AlertToasts />
              {!isStandalone ? (
                <ServerSelectionDialog
                  isOpen={isServerDialogOpen}
                  onClose={() => setIsServerDialogOpen(false)}
                  onServerAdded={() => void refresh()}
                />
              ) : null}
            </NewAppDialogProvider>
          </SidebarProvider>
        </CommandPaletteProvider>
      </InboxUnreadProvider>
    </PageTitleProvider>
  )
}
