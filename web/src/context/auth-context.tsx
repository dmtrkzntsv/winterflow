import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react"

import { apiBaseUrl } from "@/config"

type LoginPayload = {
  username: string
  password: string
}

type AuthContextValue = {
  isAuthenticated: boolean
  login: (payload: LoginPayload) => Promise<void>
  logout: () => void
}

const AuthContext = createContext<AuthContextValue | undefined>(undefined)

export function AuthProvider({ children }: { children: ReactNode }) {
  const [isAuthenticated, setIsAuthenticated] = useState(false)

  const login = useCallback(async ({ username }: LoginPayload) => {
    const loginEndpoint = `${apiBaseUrl}/auth/login`
    console.debug("[auth] login endpoint", loginEndpoint)
    const response = await fetch(loginEndpoint, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
      },
      body: JSON.stringify({ username }),
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

export function useAuth() {
  const context = useContext(AuthContext)
  if (!context) {
    throw new Error("useAuth must be used within an AuthProvider")
  }
  return context
}
