import { useMemo, useState } from "react"

import { useAppBreadcrumbs } from "@/layouts/use-app-layout"
import { Button } from "@/components/ui/button"
import { ServerSelectionDialog } from "@/components/server-creation-dialog"

export default function HomePage() {
  const [isServerDialogOpen, setIsServerDialogOpen] = useState(false)
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
      <div className="flex items-center justify-end mb-6">
        <Button onClick={() => setIsServerDialogOpen(true)} className="cursor-pointer">
          Add Server
        </Button>
      </div>
      <div className="grid auto-rows-min gap-4 md:grid-cols-3">
        <div className="bg-muted/50 aspect-video rounded-xl" />
        <div className="bg-muted/50 aspect-video rounded-xl" />
        <div className="bg-muted/50 aspect-video rounded-xl" />
      </div>
      <div className="bg-muted/50 min-h-[100vh] flex-1 rounded-xl md:min-h-min" />
      <ServerSelectionDialog
        isOpen={isServerDialogOpen}
        onClose={() => setIsServerDialogOpen(false)}
      />
    </>
  )
}
