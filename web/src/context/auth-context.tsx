import { useCallback, useEffect, useMemo, useState, type ReactNode } from "react"

import { apiBaseUrl, appBaseUrl } from "@/config"
import {
  AuthContext,
  type LoginPayload,
} from "./auth-context-base"

export function AuthProvider({ children }: { children: ReactNode }) {
  const [isAuthenticated, setIsAuthenticated] = useState(false)

  const login = useCallback(async ({ username, password }: LoginPayload) => {
    const loginEndpoint = `${apiBaseUrl}/auth/local/login?session=1`
    const origin =
      typeof window !== "undefined" ? window.location.origin : appBaseUrl
    let audienceHost = origin
    try {
      audienceHost = new URL(origin).hostname
    } catch {
      try {
        audienceHost = new URL(appBaseUrl).hostname
      } catch {
        audienceHost = origin
      }
    }

    const body = new URLSearchParams({
      user: username,
      passwd: password,
      aud: audienceHost,
    })

    const response = await fetch(loginEndpoint, {
      method: "POST",
      headers: {
        "Content-Type": "application/x-www-form-urlencoded",
      },
      body: body.toString(),
      credentials: "include",
    })
    if (!response.ok) {
      throw new Error("Failed to login")
    }
    setIsAuthenticated(true)
  }, [])

  const logout = useCallback(async () => {
    try {
      const response = await fetch(`${apiBaseUrl}/auth/logout`, {
        method: "POST",
        credentials: "include",
      })
      if (!response.ok) {
        throw new Error("Failed to logout")
      }
    } catch (error) {
      console.error("Failed to logout", error)
    } finally {
      setIsAuthenticated(false)
    }
  }, [])

  useEffect(() => {
    const fetchStatus = async () => {
      try {
        const response = await fetch(`${apiBaseUrl}/auth/status`, {
          credentials: "include",
        })
        if (!response.ok) {
          throw new Error("Failed to fetch auth status")
        }
        const data = (await response.json()) as { status?: string }
        setIsAuthenticated(data.status === "logged in")
      } catch (error) {
        console.error("Failed to fetch auth status", error)
        setIsAuthenticated(false)
      }
    }

    void fetchStatus()
  }, [])

  const value = useMemo(
    () => ({
      isAuthenticated,
      login,
      logout,
    }),
    [isAuthenticated, login, logout]
  )

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}
