import { createContext } from "react"

export type LoginPayload = {
  email: string
  password: string
}

export type AuthContextValue = {
  isAuthenticated: boolean
  // True once the initial session probe (GET /api/v1/user/get-profile)
  // has resolved. Until then the auth state is unknown and route guards
  // must not redirect.
  checked: boolean
  login: (payload: LoginPayload) => Promise<void>
  // signedIn records a session the server set some other way (register).
  signedIn: () => void
  // logout ends this browser's session; logoutAll every session of the
  // account (tokens and connected apps keep working).
  logout: () => Promise<void>
  logoutAll: () => Promise<void>
}

// LoginError is a sign-in the server refused; status is its HTTP status
// (400 or 401: the email or password is wrong).
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
