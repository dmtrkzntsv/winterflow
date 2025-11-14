import { StrictMode } from "react"
import { createRoot } from "react-dom/client"
import { createBrowserRouter, RouterProvider } from "react-router-dom"

import "@/lib/i18n"

import { RequireAuth } from "@/components/require-auth"
import { LogoSprite } from "@/components/app-logo"
import { AuthProvider } from "@/context/auth-context"
import { AppLayout } from "@/layouts/app-layout"
import HomePage from "@/pages/home"
import LoginPage from "@/pages/login"
import "./index.css"

const router = createBrowserRouter([
  {
    element: (
      <RequireAuth>
        <AppLayout />
      </RequireAuth>
    ),
    children: [
      {
        path: "/",
        element: <HomePage />,
      },
    ],
  },
  {
    path: "/login",
    element: <LoginPage />,
  },
])

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <AuthProvider>
      <LogoSprite />
      <RouterProvider router={router} />
    </AuthProvider>
  </StrictMode>
)
