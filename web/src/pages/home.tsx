import { useMemo, useState } from "react"

import { useAppBreadcrumbs } from "@/layouts/use-app-layout"
import { Button } from "@/components/ui/button"
import { ServerSelectionDialog } from "@/components/server-creation-dialog"
import { AppGrid } from "@/components/app-grid"
import { isStandalone } from "@/config"
import { useServers } from "@/context/use-servers"

export default function HomePage() {
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
        {/* Adding servers is a cloud/distributed capability. The standalone
            build ships one embedded server, so the action is disabled with a
            hint rather than hidden, to signal it exists in the cloud version. */}
        {isStandalone ? (
          <Button
            disabled
            className="cursor-not-allowed"
            title="Available in the cloud version"
          >
            Add Server
          </Button>
        ) : (
          <Button
            onClick={() => setIsServerDialogOpen(true)}
            className="cursor-pointer"
          >
            Add Server
          </Button>
        )}
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
