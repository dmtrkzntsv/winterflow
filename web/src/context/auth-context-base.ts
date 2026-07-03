import { createContext } from "react"

export type LoginPayload = {
  username: string
  password: string
}

export type AuthContextValue = {
  isAuthenticated: boolean
  // True once the initial /auth/status probe has resolved. Until then the
  // auth state is unknown and route guards must not redirect.
  checked: boolean
  login: (payload: LoginPayload) => Promise<void>
  logout: () => void
}

export const AuthContext = createContext<AuthContextValue | undefined>(
  undefined
)
