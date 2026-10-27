import type { ReactNode } from "react"
import { Navigate, useLocation } from "react-router-dom"

import { useAuth } from "@/context/use-auth"
import { loginPath } from "@/lib/return-path"

type RequireAuthProps = {
  children: ReactNode
}

// RequireAuth shows children to a signed-in browser and sends anyone else
// to /login?next=<where they were headed>: a URL, not router state, so the
// destination also survives a provider sign-in's round trip (an MCP
// client's /oauth/authorize?… consent link, an app deep link).
export function RequireAuth({ children }: RequireAuthProps) {
  const { isAuthenticated, checked } = useAuth()
  const location = useLocation()

  // Until the auth probe resolves the state is unknown — redirecting now
  // would bounce deep links (e.g. a reload on /app/:id) through /login and
  // lose the destination.
  if (!checked) {
    return null
  }

  if (!isAuthenticated) {
    return (
      <Navigate
        to={loginPath(`${location.pathname}${location.search}${location.hash}`)}
        state={{ from: location }}
        replace
      />
    )
  }

  return <>{children}</>
}
