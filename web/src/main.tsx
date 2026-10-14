import { StrictMode } from "react"
import { createRoot } from "react-dom/client"
import { createBrowserRouter, Outlet, RouterProvider } from "react-router-dom"

import "@/styles/inter.css"
import "@/lib/i18n"

import { RequireAuth } from "@/components/require-auth"
import { LogoSprite } from "@/components/app-logo"
import { ThemedToaster } from "@/components/themed-toaster"
import { AuthProvider } from "@/context/auth-context"
import { UserProvider } from "@/context/user-context"
import { ProfileProvider } from "@/context/profile-context"
import { ServersProvider } from "@/context/servers-context"
import { NotificationsProvider } from "@/context/notifications-context"
import { AppsProvider } from "@/context/apps-context"
import { AppLayout } from "@/layouts/app-layout"
import LoginPage from "@/pages/login"
import RegisterPage from "@/pages/register"
import { startTheme } from "@/lib/theme"
import { appRoutes } from "@/routes"
import "./index.css"

// Light, dark or the system's (the user menu and ⌘K switch it); applied
// before the first render so nothing flashes.
startTheme()

// The servers and apps providers sit inside the router: the active server is
// read from the URL. The signed-in pages are the route table in routes.tsx.
const router = createBrowserRouter([
  {
    element: (
      <ServersProvider>
        <AppsProvider>
          <Outlet />
        </AppsProvider>
      </ServersProvider>
    ),
    children: [
      {
        element: (
          <RequireAuth>
            <AppLayout />
          </RequireAuth>
        ),
        children: appRoutes,
      },
      {
        path: "/login",
        element: <LoginPage />,
      },
      {
        path: "/register",
        element: <RegisterPage />,
      },
    ],
  },
])

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <AuthProvider>
      <UserProvider>
        <ProfileProvider>
        <NotificationsProvider>
          <LogoSprite />
          <RouterProvider router={router} />
          <ThemedToaster />
        </NotificationsProvider>
        </ProfileProvider>
      </UserProvider>
    </AuthProvider>
  </StrictMode>
)
