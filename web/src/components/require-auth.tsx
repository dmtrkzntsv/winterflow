import type { ReactNode } from "react"
import { Navigate, useLocation } from "react-router-dom"

import { useAuth } from "@/context/use-auth"

type RequireAuthProps = {
  children: ReactNode
}

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
    return <Navigate to="/login" state={{ from: location }} replace />
  }

  return <>{children}</>
}
