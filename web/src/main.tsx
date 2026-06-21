import { StrictMode } from "react"
import { createRoot } from "react-dom/client"
import { createBrowserRouter, RouterProvider } from "react-router-dom"

import "@/lib/i18n"

import { RequireAuth } from "@/components/require-auth"
import { LogoSprite } from "@/components/app-logo"
import { Toaster } from "@/components/ui/sonner"
import { AuthProvider } from "@/context/auth-context"
import { UserProvider } from "@/context/user-context"
import { ServersProvider } from "@/context/servers-context"
import { NotificationsProvider } from "@/context/notifications-context"
import { AppsProvider } from "@/context/apps-context"
import { AppLayout } from "@/layouts/app-layout"
import HomePage from "@/pages/home"
import CreateAppPage from "@/pages/create-app"
import AppDetailsPage from "@/pages/app-details"
import SettingsPage from "@/pages/settings"
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
      {
        path: "/apps/new",
        element: <CreateAppPage />,
      },
      {
        path: "/apps/:appId/edit",
        element: <CreateAppPage />,
      },
      {
        path: "/app/:appId",
        element: <AppDetailsPage />,
      },
      {
        path: "/settings",
        element: <SettingsPage />,
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
      <UserProvider>
        <NotificationsProvider>
          <ServersProvider>
            <AppsProvider>
              <LogoSprite />
              <RouterProvider router={router} />
              <Toaster />
            </AppsProvider>
          </ServersProvider>
        </NotificationsProvider>
      </UserProvider>
    </AuthProvider>
  </StrictMode>
)
