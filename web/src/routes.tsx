import type { RouteObject } from "react-router-dom";

import { LegacyRedirect } from "@/components/legacy-redirect";
import { NotFound } from "@/components/not-found";
import { DefaultServer, ServerScope } from "@/components/server-routes";
import { SettingsIndex, SettingsLayout } from "@/layouts/settings-layout";
import {
  INBOX_PATH,
  NEW_TEMPLATE_PATH,
  SETTINGS_PATH,
  TEMPLATES_PATH,
  legacyAlertsTarget,
  legacyServerSettingsTarget,
  newAppPath,
  settingsPath,
  templatePath,
} from "@/lib/routes";
import AppDetailsPage from "@/pages/app-details";
import HomePage from "@/pages/home";
import InboxPage from "@/pages/inbox";
import MonitoringPage from "@/pages/monitoring";
import NewAppPage from "@/pages/new-app";
import OrgMembersPage from "@/pages/org-members";
import OrgTemplatesPage from "@/pages/org-templates";
import {
  ServerDockerPage,
  ServerDomainsPage,
  ServerGeneralPage,
  ServerStoragesPage,
} from "@/pages/server-settings";
import NotificationsSettingsPage from "@/pages/settings-notifications";
import SharedVariablesSettingsPage from "@/pages/settings-shared-variables";
import TemplatePage from "@/pages/template-page";
import UserPasswordPage from "@/pages/user-password";
import UserTokensPage from "@/pages/user-tokens";

// appRoutes is everything inside the signed-in shell (AppLayout): the
// route table of the UX rebuild (spec §2), then every pre-rebuild URL sent
// to its new place, so bookmarks, alert e-mails and open tabs keep working.
export const appRoutes: RouteObject[] = [
  { path: "/", element: <DefaultServer fallback={<HomePage />} /> },
  {
    path: "/server/:serverId",
    element: <ServerScope />,
    children: [
      { index: true, element: <HomePage /> },
      { path: "new", element: <NewAppPage /> },
      { path: "app/:appId", element: <AppDetailsPage /> },
      { path: "monitoring", element: <MonitoringPage /> },
      {
        path: "settings",
        element: <SettingsLayout />,
        children: [
          // The pre-rebuild tabbed page: ?tab= picks the section.
          {
            index: true,
            element: (
              <LegacyRedirect
                to={({ params, search }) => legacyServerSettingsTarget(params.serverId ?? "", search.get("tab"))}
              />
            ),
          },
          { path: "general", element: <ServerGeneralPage /> },
          { path: "domains", element: <ServerDomainsPage /> },
          { path: "storages", element: <ServerStoragesPage /> },
          { path: "docker", element: <ServerDockerPage /> },
        ],
      },
      // Before the rebuild: Add app and From template.
      { path: "create-app", element: <LegacyRedirect to={({ params }) => newAppPath(params.serverId ?? "")} /> },
      {
        path: "create-app/template",
        element: <LegacyRedirect to={({ params }) => newAppPath(params.serverId ?? "", "template")} />,
      },
    ],
  },
  { path: INBOX_PATH, element: <InboxPage /> },
  { path: TEMPLATES_PATH, element: <OrgTemplatesPage /> },
  { path: NEW_TEMPLATE_PATH, element: <TemplatePage /> },
  { path: `${TEMPLATES_PATH}/:templateId`, element: <TemplatePage /> },
  {
    path: SETTINGS_PATH,
    element: <SettingsLayout />,
    children: [
      { index: true, element: <SettingsIndex /> },
      { path: "organization", element: <OrgMembersPage section="organization" /> },
      { path: "members", element: <OrgMembersPage section="members" /> },
      { path: "variables", element: <SharedVariablesSettingsPage /> },
      { path: "notifications", element: <NotificationsSettingsPage /> },
      { path: "password", element: <UserPasswordPage /> },
      { path: "tokens", element: <UserTokensPage /> },
    ],
  },
  // Before the rebuild: the organization's and the user's pages.
  { path: "/org/members", element: <LegacyRedirect to={settingsPath("members")} /> },
  { path: "/org/variables", element: <LegacyRedirect to={settingsPath("variables")} /> },
  {
    path: "/org/alerts",
    element: <LegacyRedirect to={({ search }) => legacyAlertsTarget(search.get("tab"))} />,
  },
  { path: "/org/templates", element: <LegacyRedirect to={TEMPLATES_PATH} /> },
  { path: "/org/templates/new", element: <LegacyRedirect to={NEW_TEMPLATE_PATH} /> },
  {
    path: "/org/templates/:templateId",
    element: <LegacyRedirect to={({ params }) => templatePath(params.templateId ?? "")} />,
  },
  { path: "/user/password", element: <LegacyRedirect to={settingsPath("password")} /> },
  { path: "/user/tokens", element: <LegacyRedirect to={settingsPath("tokens")} /> },
  { path: "*", element: <NotFound /> },
];
