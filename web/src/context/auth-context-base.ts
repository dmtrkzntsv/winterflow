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

// LoginError is a sign-in the server refused; status is its HTTP status
// (400, 401 or 403: the email or password is wrong).
export class LoginError extends Error {
  status: number
  constructor(status: number) {
    super("Failed to login")
    this.name = "LoginError"
    this.status = status
  }
}

export const AuthContext = createContext<AuthContextValue | undefined>(
  undefined
)
