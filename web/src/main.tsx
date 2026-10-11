import { StrictMode } from "react"
import { createRoot } from "react-dom/client"
import { createBrowserRouter, Outlet, RouterProvider } from "react-router-dom"

import "@/lib/i18n"

import { RequireAuth } from "@/components/require-auth"
import { ServerScope, ToServerScope } from "@/components/server-routes"
import { LogoSprite } from "@/components/app-logo"
import { Toaster } from "@/components/ui/sonner"
import { AuthProvider } from "@/context/auth-context"
import { UserProvider } from "@/context/user-context"
import { ProfileProvider } from "@/context/profile-context"
import { ServersProvider } from "@/context/servers-context"
import { NotificationsProvider } from "@/context/notifications-context"
import { AppsProvider } from "@/context/apps-context"
import { AppLayout } from "@/layouts/app-layout"
import HomePage from "@/pages/home"
import CreateAppPage from "@/pages/create-app"
import AppDetailsPage from "@/pages/app-details"
import SettingsPage from "@/pages/settings"
import UserTokensPage from "@/pages/user-tokens"
import UserPasswordPage from "@/pages/user-password"
import OrgMembersPage from "@/pages/org-members"
import LoginPage from "@/pages/login"
import RegisterPage from "@/pages/register"
import "./index.css"

// The servers and apps providers sit inside the router: the active server is
// read from the URL.
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
        children: [
          {
            path: "/",
            element: <ToServerScope fallback={<HomePage />} />,
          },
          {
            path: "/server/:serverId",
            element: <ServerScope />,
            children: [
              { index: true, element: <HomePage /> },
              { path: "create-app", element: <CreateAppPage /> },
              { path: "app/:appId", element: <AppDetailsPage /> },
              { path: "settings", element: <SettingsPage /> },
            ],
          },
          // Pre-scoping URLs (bookmarks, old links) move into the active server.
          { path: "/create-app", element: <ToServerScope /> },
          { path: "/app/:appId", element: <ToServerScope /> },
          { path: "/settings", element: <ToServerScope /> },
          {
            path: "/user/tokens",
            element: <UserTokensPage />,
          },
          {
            path: "/user/password",
            element: <UserPasswordPage />,
          },
          {
            path: "/org/members",
            element: <OrgMembersPage />,
          },
        ],
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
          <Toaster />
        </NotificationsProvider>
        </ProfileProvider>
      </UserProvider>
    </AuthProvider>
  </StrictMode>
)
