import { render, screen } from "@testing-library/react";
import { useEffect } from "react";
import { Outlet, RouterProvider, createMemoryRouter, useLocation, useParams } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { AppLayout } from "@/layouts/app-layout";
import { appRoutes } from "@/routes";

type FakeServer = { id: string; name: string; features?: Record<string, boolean> };
const ALPHA: FakeServer = { id: "s1", name: "Alpha", features: { ingress: true } };
let servers: FakeServer[] = [ALPHA];
let mustChangePassword = false;

vi.mock("@/context/use-servers", () => ({
  useServers: () => ({
    servers,
    activeServer: servers[0] ?? null,
    activeServerId: servers[0]?.id ?? null,
    loading: false,
    refresh: vi.fn(),
  }),
}));
vi.mock("@/context/use-profile", () => ({
  useProfile: () => ({
    profile: { user_id: "u1", name: "Ann", email: "ann@example.test", must_change_password: mustChangePassword },
  }),
}));

// The shell around the pages is not under test here.
vi.mock("@/components/command-palette", () => ({ CommandPalette: () => null }));
vi.mock("@/components/app-sidebar", () => ({ AppSidebar: () => null }));
vi.mock("@/components/alerts/alert-toasts", () => ({ AlertToasts: () => null }));
vi.mock("@/components/server-creation-dialog", () => ({ ServerSelectionDialog: () => null }));

// Every page is a stub naming itself: this test is about which page an
// address opens.
vi.mock("@/pages/home", () => ({ default: () => <p>apps page</p> }));
vi.mock("@/pages/app-details", () => ({ default: () => <p>app page</p> }));
vi.mock("@/pages/monitoring", () => ({ default: () => <p>monitoring page</p> }));
vi.mock("@/pages/new-app", () => ({
  default: function NewAppStub() {
    return <p>new app page {new URLSearchParams(useLocation().search).get("from") ?? "chooser"}</p>;
  },
}));
vi.mock("@/pages/inbox", () => ({ default: () => <p>inbox page</p> }));
vi.mock("@/pages/org-members", () => ({
  default: ({ section }: { section?: string }) => <p>org page {section}</p>,
}));
vi.mock("@/pages/org-templates", () => ({ default: () => <p>templates page</p> }));
vi.mock("@/pages/template-page", () => ({
  default: function TemplateStub() {
    return <p>template page {useParams().templateId ?? "new"}</p>;
  },
}));
vi.mock("@/pages/server-settings", () => ({
  ServerGeneralPage: () => <p>server general</p>,
  ServerDomainsPage: () => <p>server domains</p>,
  ServerStoragesPage: () => <p>server storages</p>,
  ServerDockerPage: () => <p>server docker</p>,
}));
vi.mock("@/pages/settings-notifications", () => ({ default: () => <p>notifications page</p> }));
vi.mock("@/pages/settings-shared-variables", () => ({ default: () => <p>shared variables page</p> }));
vi.mock("@/pages/user-password", () => ({ default: () => <p>password page</p> }));
vi.mock("@/pages/user-tokens", () => ({ default: () => <p>tokens page</p> }));

let visited: string[] = [];

function Where() {
  const { pathname, search, hash } = useLocation();
  const here = pathname + search + hash;
  useEffect(() => {
    visited.push(here);
  }, [here]);
  return <span data-testid="at">{here}</span>;
}

function renderAt(url: string, { shell = false } = {}) {
  const router = createMemoryRouter(
    [
      {
        element: (
          <>
            <Outlet />
            <Where />
          </>
        ),
        children: shell ? [{ element: <AppLayout />, children: appRoutes }] : appRoutes,
      },
    ],
    { initialEntries: [url] },
  );
  return render(<RouterProvider router={router} />);
}

const at = () => screen.getByTestId("at");

describe("route table", () => {
  beforeEach(() => {
    visited = [];
  });
  afterEach(() => {
    servers = [ALPHA];
    mustChangePassword = false;
  });

  it.each([
    ["/server/s1", "apps page"],
    ["/server/s1/new", "new app page chooser"],
    ["/server/s1/new?from=compose", "new app page compose"],
    ["/server/s1/new?from=template", "new app page template"],
    ["/server/s1/new?from=git", "new app page git"],
    ["/server/s1/app/a1", "app page"],
    ["/server/s1/monitoring", "monitoring page"],
    ["/server/s1/settings/general", "server general"],
    ["/server/s1/settings/domains", "server domains"],
    ["/server/s1/settings/storages", "server storages"],
    ["/server/s1/settings/docker", "server docker"],
    ["/inbox", "inbox page"],
    ["/templates", "templates page"],
    ["/templates/new", "template page new"],
    ["/templates/t1", "template page t1"],
    ["/settings/organization", "org page organization"],
    ["/settings/members", "org page members"],
    ["/settings/variables", "shared variables page"],
    ["/settings/notifications", "notifications page"],
    ["/settings/password", "password page"],
    ["/settings/tokens", "tokens page"],
  ])("opens %s", async (url, page) => {
    renderAt(url);
    expect(await screen.findByText(page)).toBeInTheDocument();
    expect(at()).toHaveTextContent(url);
  });

  it("keeps the alert e-mails' deep links as they are", async () => {
    renderAt("/server/s1/app/a1?tab=metrics");
    expect(await screen.findByText("app page")).toBeInTheDocument();
    expect(at()).toHaveTextContent("/server/s1/app/a1?tab=metrics");
  });

  it.each([
    ["/", "/server/s1", "apps page"],
    ["/settings", "/server/s1/settings/general", "server general"],
    ["/server/s1/settings", "/server/s1/settings/general", "server general"],
    ["/server/s1/settings?tab=agent", "/server/s1/settings/general", "server general"],
    ["/server/s1/settings?tab=registries", "/server/s1/settings/docker", "server docker"],
    ["/server/s1/settings?tab=networks", "/server/s1/settings/docker", "server docker"],
    ["/server/s1/settings?tab=volumes", "/server/s1/settings/docker", "server docker"],
    ["/server/s1/settings?tab=domains", "/server/s1/settings/domains", "server domains"],
    ["/server/s1/settings?tab=storages", "/server/s1/settings/storages", "server storages"],
    ["/server/s1/settings?tab=variables", "/settings/variables", "shared variables page"],
    ["/server/s1/create-app", "/server/s1/new?from=compose", "new app page compose"],
    ["/server/s1/create-app/template", "/server/s1/new?from=template", "new app page template"],
    ["/org/members", "/settings/members", "org page members"],
    ["/org/variables", "/settings/variables", "shared variables page"],
    ["/org/alerts", "/inbox", "inbox page"],
    ["/org/alerts?tab=history", "/inbox", "inbox page"],
    ["/org/alerts?tab=rules", "/settings/notifications", "notifications page"],
    ["/org/alerts?tab=channels", "/settings/notifications", "notifications page"],
    ["/org/templates", "/templates", "templates page"],
    ["/org/templates/new", "/templates/new", "template page new"],
    ["/org/templates/t1", "/templates/t1", "template page t1"],
    ["/org/templates/t1#versions", "/templates/t1#versions", "template page t1"],
    ["/user/password", "/settings/password", "password page"],
    ["/user/tokens", "/settings/tokens", "tokens page"],
  ])("sends %s to %s", async (from, to, page) => {
    renderAt(from);
    expect(await screen.findByText(page)).toBeInTheDocument();
    expect(at().textContent).toBe(to);
  });

  // The old ?tab= only chose the target: it must not follow to a page that
  // would read it as one of its own tabs. Other parameters stay.
  it.each([
    ["/org/alerts?tab=rules", "/settings/notifications", "notifications page"],
    ["/org/alerts?tab=history&x=1", "/inbox?x=1", "inbox page"],
    ["/server/s1/settings?tab=volumes", "/server/s1/settings/docker", "server docker"],
    ["/server/s1/settings?tab=volumes&x=1", "/server/s1/settings/docker?x=1", "server docker"],
    ["/server/s1/create-app?tab=x&y=2", "/server/s1/new?from=compose&y=2", "new app page compose"],
    ["/org/members?tab=x", "/settings/members", "org page members"],
    ["/org/templates/t1?tab=x#versions", "/templates/t1#versions", "template page t1"],
  ])("sends %s to %s without a stale tab", async (from, to, page) => {
    renderAt(from);
    expect(await screen.findByText(page)).toBeInTheDocument();
    expect(at().textContent).toBe(to);
    expect(at().textContent).not.toContain("tab=");
  });

  it("opens the organization's settings at /settings while there is no server", async () => {
    servers = [];
    renderAt("/settings");
    expect(await screen.findByText("org page organization")).toBeInTheDocument();
  });

  it.each(["/nowhere", "/server/s1/settings/nope", "/org/nothing"])("answers %s with Page not found", async (url) => {
    renderAt(url);
    expect(await screen.findByText("Page not found")).toBeInTheDocument();
  });

  describe("a user on a temporary password", () => {
    beforeEach(() => {
      mustChangePassword = true;
    });

    it("goes from /user/password to /settings/password in one hop, without a loop", async () => {
      renderAt("/user/password", { shell: true });
      expect(await screen.findByText("password page")).toBeInTheDocument();
      expect(visited).toEqual(["/user/password", "/settings/password"]);
    });

    it("is sent from any other page to /settings/password and stays there", async () => {
      renderAt("/server/s1", { shell: true });
      expect(await screen.findByText("password page")).toBeInTheDocument();
      expect(visited).toEqual(["/server/s1", "/settings/password"]);
    });
  });
});
