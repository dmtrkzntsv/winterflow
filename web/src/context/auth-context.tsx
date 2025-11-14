import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react"

const AUTH_TOKEN_KEY = "wf_auth_token"

type LoginPayload = {
  email: string
  password: string
}

type AuthContextValue = {
  isAuthenticated: boolean
  login: (payload: LoginPayload) => Promise<void>
  logout: () => void
}

const AuthContext = createContext<AuthContextValue | undefined>(undefined)

const readStoredToken = () => {
  if (typeof window === "undefined") {
    return null
  }
  return window.localStorage.getItem(AUTH_TOKEN_KEY)
}

export function AuthProvider({ children }: { children: ReactNode }) {
  const [isAuthenticated, setIsAuthenticated] = useState(
    () => Boolean(readStoredToken()) // hydrate from localStorage
  )

  const login = useCallback(async ({ email }: LoginPayload) => {
    const token = window.btoa(email)
    window.localStorage.setItem(AUTH_TOKEN_KEY, token)
    setIsAuthenticated(true)
  }, [])

  const logout = useCallback(() => {
    window.localStorage.removeItem(AUTH_TOKEN_KEY)
    setIsAuthenticated(false)
  }, [])

  useEffect(() => {
    const handleStorage = (event: StorageEvent) => {
      if (event.key === AUTH_TOKEN_KEY) {
        setIsAuthenticated(Boolean(event.newValue))
      }
    }
    window.addEventListener("storage", handleStorage)
    return () => window.removeEventListener("storage", handleStorage)
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
