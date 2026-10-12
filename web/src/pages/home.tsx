import { useMemo, useState } from "react"
import { useNavigate } from "react-router-dom"
import { Plus } from "lucide-react"

import { useAppBreadcrumbs } from "@/layouts/use-app-layout"
import { Button } from "@/components/ui/button"
import { ServerSelectionDialog } from "@/components/server-creation-dialog"
import { ServerCards } from "@/components/server-cards"
import { AppGrid } from "@/components/app-grid"
import { isStandalone } from "@/config"
import { useServers } from "@/context/use-servers"
import { useServerPath } from "@/context/use-server-path"

export default function HomePage() {
  const navigate = useNavigate()
  const toServer = useServerPath()
  const [isServerDialogOpen, setIsServerDialogOpen] = useState(false)
  const { refresh } = useServers()
  const breadcrumbs = useMemo(
    () => [
      { label: "Dashboard", href: "#", hideOnMobile: true },
      { label: "Apps" },
    ],
    []
  )
  useAppBreadcrumbs(breadcrumbs)

  return (
    <div className="space-y-4">
      {/* The apps page: title and New App share one row. */}
      <div className="flex items-center justify-between gap-3">
        <h1 className="text-2xl font-semibold tracking-tight">Apps</h1>
        <Button size="sm" onClick={() => navigate(toServer("/create-app"))}>
          <Plus className="size-4" /> New App
        </Button>
      </div>

      {/* Server health at a glance: a compact strip above the apps. Adding
          servers is a cloud capability; the standalone build ships one
          embedded server, so the action is hidden. */}
      <section aria-label="Servers" className="space-y-2">
        <div className="flex items-center justify-between">
          <h2 className="text-sm font-medium text-muted-foreground">Servers</h2>
          {!isStandalone ? (
            <Button
              variant="ghost"
              size="sm"
              onClick={() => setIsServerDialogOpen(true)}
            >
              <Plus className="size-4" /> Add Server
            </Button>
          ) : null}
        </div>
        <ServerCards />
      </section>

      <AppGrid />

      {!isStandalone ? (
        <ServerSelectionDialog
          isOpen={isServerDialogOpen}
          onClose={() => setIsServerDialogOpen(false)}
          onServerAdded={() => void refresh()}
        />
      ) : null}
    </div>
  )
}
