import { useMemo } from "react"

import { useAppBreadcrumbs } from "@/layouts/use-app-layout"

export default function HomePage() {
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
      <div className="grid auto-rows-min gap-4 md:grid-cols-3">
        <div className="bg-muted/50 aspect-video rounded-xl" />
        <div className="bg-muted/50 aspect-video rounded-xl" />
        <div className="bg-muted/50 aspect-video rounded-xl" />
      </div>
      <div className="bg-muted/50 min-h-[100vh] flex-1 rounded-xl md:min-h-min" />
    </>
  )
}
