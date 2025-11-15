import { createContext } from "react"

export type LoginPayload = {
  username: string
  password: string
}

export type AuthContextValue = {
  isAuthenticated: boolean
  login: (payload: LoginPayload) => Promise<void>
  logout: () => void
}

export const AuthContext = createContext<AuthContextValue | undefined>(
  undefined
)
