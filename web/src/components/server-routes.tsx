import type { ReactNode } from "react"
import { Navigate, Outlet, useLocation, useParams } from "react-router-dom"

import { useServers } from "@/context/use-servers"
import { serverPath } from "@/lib/server-paths"

// ServerScope guards /server/:serverId/*: an id the org doesn't have (stale
// bookmark, removed server) goes back to the default server.
export function ServerScope() {
  const { serverId } = useParams()
  const { servers, loading } = useServers()

  if (loading && servers.length === 0) return null
  if (!servers.some((s) => s.id === serverId)) {
    return <Navigate to="/" replace />
  }
  return <Outlet />
}

// ToServerScope moves an unscoped URL (/, or a pre-scoping /settings, /app/:id
// bookmark) into the active server's scope. With no server to scope to it
// renders fallback (the bare dashboard at /) or goes to /.
export function ToServerScope({ fallback }: { fallback?: ReactNode }) {
  const { pathname, search } = useLocation()
  const { activeServerId, servers, loading } = useServers()

  if (loading && servers.length === 0) return null
  if (activeServerId) {
    const sub = pathname === "/" ? "" : pathname
    return <Navigate to={serverPath(activeServerId, sub) + search} replace />
  }
  return fallback ?? <Navigate to="/" replace />
}
