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

export default function HomePage() {
  const navigate = useNavigate()
  const [isServerDialogOpen, setIsServerDialogOpen] = useState(false)
  const { refresh } = useServers()
  const breadcrumbs = useMemo(
    () => [
      { label: "Overview", href: "#", hideOnMobile: true },
      { label: "Dashboard" },
    ],
    []
  )
  useAppBreadcrumbs(breadcrumbs)

  return (
    <div className="space-y-4">
      <h1 className="text-2xl font-semibold tracking-tight">Dashboard</h1>
      {/* Servers row (v1 AgentList). Adding servers is a cloud capability; the
          standalone build ships one embedded server, so the action is hidden. */}
      <div className="flex items-center justify-between pt-2">
        <h2 className="text-sm font-medium text-muted-foreground">Servers</h2>
        {!isStandalone ? (
          <Button
            variant="outline"
            size="sm"
            onClick={() => setIsServerDialogOpen(true)}
          >
            <Plus className="size-4" /> Add Server
          </Button>
        ) : null}
      </div>
      <ServerCards />

      {/* Apps: the same section shape as Servers, each app its own card. */}
      <div className="flex items-center justify-between pt-4">
        <h2 className="text-sm font-medium text-muted-foreground">Apps</h2>
        <Button size="sm" onClick={() => navigate("/create-app")}>
          <Plus className="size-4" /> New App
        </Button>
      </div>
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
