import type { ReactNode } from "react"
import { Navigate, Outlet, useOutletContext, useParams } from "react-router-dom"

import { NotFound } from "@/components/not-found"
import { useServers } from "@/context/use-servers"
import { serverPath } from "@/lib/server-paths"

// ServerScope guards /server/:serverId/*: an id the org doesn't have (stale
// bookmark, removed server) is an error page, never a silent switch.
export function ServerScope() {
  const { serverId } = useParams()
  const { servers, loading } = useServers()
  // Hand any outlet context of the layout through to the pages: a nested
  // <Outlet /> without it would shadow the layout's with undefined.
  const layout = useOutletContext()

  if (loading && servers.length === 0) return null
  if (!servers.some((s) => s.id === serverId)) {
    return (
      <NotFound
        title="Server not found"
        description="This server doesn't exist or isn't in your organization."
      />
    )
  }
  return <Outlet context={layout} />
}

// DefaultServer is the entry point at /: it opens the active server's
// dashboard, or renders fallback while the org has no server yet.
export function DefaultServer({ fallback }: { fallback: ReactNode }) {
  const { activeServerId, servers, loading } = useServers()

  if (loading && servers.length === 0) return null
  if (activeServerId) {
    return <Navigate to={serverPath(activeServerId)} replace />
  }
  return fallback
}
