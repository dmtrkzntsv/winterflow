import { StrictMode } from "react"
import { createRoot } from "react-dom/client"
import { createBrowserRouter, Outlet, RouterProvider } from "react-router-dom"

import "@/styles/inter.css"
import "@/lib/i18n"

import { RequireAuth } from "@/components/require-auth"
import { DefaultServer, ServerScope } from "@/components/server-routes"
import { NotFound } from "@/components/not-found"
import { LogoSprite } from "@/components/app-logo"
import { ThemedToaster } from "@/components/themed-toaster"
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
import MonitoringPage from "@/pages/monitoring"
import SettingsPage from "@/pages/settings"
import UserTokensPage from "@/pages/user-tokens"
import UserPasswordPage from "@/pages/user-password"
import OrgMembersPage from "@/pages/org-members"
import OrgAlertsPage from "@/pages/org-alerts"
import OrgVariablesPage from "@/pages/org-variables";
import OrgTemplatesPage from "@/pages/org-templates"
import TemplatePage from "@/pages/template-page"
import CreateFromTemplatePage from "@/pages/create-from-template"
import LoginPage from "@/pages/login"
import RegisterPage from "@/pages/register"
import { startTheme } from "@/lib/theme"
import "./index.css"

// Light, dark or the system's (the user menu and ⌘K switch it); applied
// before the first render so nothing flashes.
startTheme()

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
            element: <DefaultServer fallback={<HomePage />} />,
          },
          {
            path: "/server/:serverId",
            element: <ServerScope />,
            children: [
              { index: true, element: <HomePage /> },
              { path: "create-app", element: <CreateAppPage /> },
              { path: "create-app/template", element: <CreateFromTemplatePage /> },
              { path: "app/:appId", element: <AppDetailsPage /> },
              { path: "monitoring", element: <MonitoringPage /> },
              { path: "settings", element: <SettingsPage /> },
            ],
          },
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
          {
            path: "/org/alerts",
            element: <OrgAlertsPage />,
          },
          {
            path: "/org/variables",
            element: <OrgVariablesPage />,
          },
          {
            path: "/org/templates",
            element: <OrgTemplatesPage />,
          },
          {
            path: "/org/templates/new",
            element: <TemplatePage />,
          },
          {
            path: "/org/templates/:templateId",
            element: <TemplatePage />,
          },
          { path: "*", element: <NotFound /> },
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
          <ThemedToaster />
        </NotificationsProvider>
        </ProfileProvider>
      </UserProvider>
    </AuthProvider>
  </StrictMode>
)
