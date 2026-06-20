import { useMemo, useState } from "react"

import { useAppBreadcrumbs } from "@/layouts/use-app-layout"
import { Button } from "@/components/ui/button"
import { ServerSelectionDialog } from "@/components/server-creation-dialog"
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
      <div className="grid auto-rows-min gap-4 md:grid-cols-3">
        <div className="bg-muted/50 aspect-video rounded-xl" />
        <div className="bg-muted/50 aspect-video rounded-xl" />
        <div className="bg-muted/50 aspect-video rounded-xl" />
      </div>
      <div className="bg-muted/50 min-h-[100vh] flex-1 rounded-xl md:min-h-min" />
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
