import { useMemo, useState } from "react"
import { useNavigate } from "react-router-dom"
import { Plus } from "lucide-react"

import { useAppBreadcrumbs } from "@/layouts/use-app-layout"
import { Button } from "@/components/ui/button"
import { ServerSelectionDialog } from "@/components/server-creation-dialog"
import { AppGrid } from "@/components/app-grid"
import { isStandalone } from "@/config"
import { useServers } from "@/context/use-servers"

export default function HomePage() {
  const navigate = useNavigate()
  const [isServerDialogOpen, setIsServerDialogOpen] = useState(false)
  const { servers, loading, refresh } = useServers()
  const breadcrumbs = useMemo(
    () => [
      { label: "Overview", href: "#", hideOnMobile: true },
      { label: "Welcome" },
    ],
    []
  )
  useAppBreadcrumbs(breadcrumbs)

  return (
    <>
      <div className="flex items-center justify-between mb-6">
        <p className="text-sm text-muted-foreground">
          {loading
            ? "Loading servers…"
            : `${servers.length} server${servers.length === 1 ? "" : "s"}`}
        </p>
        <div className="flex gap-2">
          {/* Adding servers is a cloud/distributed capability. The standalone
              build ships one embedded server, so the action is omitted there. */}
          {!isStandalone ? (
            <Button
              variant="outline"
              onClick={() => setIsServerDialogOpen(true)}
              className="cursor-pointer"
            >
              Add Server
            </Button>
          ) : null}
          <Button
            onClick={() => navigate("/apps/new")}
            className="cursor-pointer"
          >
            <Plus className="size-4" /> New App
          </Button>
        </div>
      </div>
      <AppGrid />
      {!isStandalone ? (
        <ServerSelectionDialog
          isOpen={isServerDialogOpen}
          onClose={() => setIsServerDialogOpen(false)}
          onServerAdded={() => void refresh()}
        />
      ) : null}
    </>
  )
}
