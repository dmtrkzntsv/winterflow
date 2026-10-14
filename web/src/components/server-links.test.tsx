import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Outlet, Route, Routes } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import { AppSidebar } from "@/components/app-sidebar";
import { DefaultServer } from "@/components/server-routes";
import { TooltipProvider } from "@/components/ui/tooltip";
import { SidebarContext, type SidebarContextProps } from "@/components/ui/use-sidebar";
import { NotificationsContext, type NotificationsContextValue } from "@/context/notifications-context-base";
import HomePage from "@/pages/home";
import { fakeAPI } from "@/test/fake-api";

// Every link and switch on the dashboard and in the sidebar stays inside the
// active server's scope (/server/:id/...).

const selectServer = vi.fn();
let servers = [
  { id: "s1", organizationId: "o", name: "Alpha", createdAt: "", lastSeenAt: null, capabilities: {} },
  { id: "s2", organizationId: "o", name: "Beta", createdAt: "", lastSeenAt: null, capabilities: {} },
];
vi.mock("@/context/use-servers", () => ({
  useServers: () => ({
    servers,
    activeServer: servers[0] ?? null,
    activeServerId: servers[0]?.id ?? null,
    selectServer,
    statusByServer: {},
    loading: false,
    refresh: vi.fn(),
  }),
}));
vi.mock("@/context/use-apps", () => ({
  useApps: () => ({
    apps: [{ id: "a1", serverId: "s1", name: "Shop", version: 1, createdAt: "" }],
    statusByApp: {},
    loading: false,
    control: vi.fn(),
  }),
}));
vi.mock("@/hooks/use-inbox-unread", () => ({
  useInboxUnread: () => ({ count: 0, seenAt: 0, isUnread: () => false, markAllRead: () => {} }),
}));
// NavUser needs the account contexts; it has its own test.
vi.mock("@/components/nav-user", () => ({ NavUser: () => null }));

// jsdom has no matchMedia, which SidebarProvider needs: hand the sidebar a
// fixed desktop state instead.
const sidebar: SidebarContextProps = {
  state: "expanded",
  open: true,
  setOpen: () => {},
  openMobile: false,
  setOpenMobile: () => {},
  isMobile: false,
  toggleSidebar: () => {},
};

// New app opens the chooser, which reads the organization's templates.
const notifications = { connected: true, subscribe: () => () => {}, waitFor: vi.fn() } as NotificationsContextValue;

function renderAt(url: string) {
  return render(
    <MemoryRouter initialEntries={[url]}>
      <NotificationsContext.Provider value={notifications}>
        <TooltipProvider>
          <SidebarContext.Provider value={sidebar}>
            <AppSidebar />
            <Routes>
              <Route element={<Outlet />}>
                <Route path="/" element={<DefaultServer fallback={<p>no servers</p>} />} />
                <Route path="/server/s1" element={<main><HomePage /></main>} />
                <Route path="/server/s1/new" element={<p>create page</p>} />
              </Route>
            </Routes>
          </SidebarContext.Provider>
        </TooltipProvider>
      </NotificationsContext.Provider>
    </MemoryRouter>,
  );
}

describe("server-scoped links", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("points the sidebar and the app grid into the active server", async () => {
    renderAt("/server/s1");
    expect(screen.getByRole("link", { name: "Apps" })).toHaveAttribute("href", "/server/s1");
    // "Monitoring" is in the sidebar and in the health strip: both lead into s1.
    const monitoring = screen.getAllByRole("link", { name: "Monitoring" });
    expect(monitoring.length).toBeGreaterThanOrEqual(2);
    for (const link of monitoring) expect(link).toHaveAttribute("href", "/server/s1/monitoring");
    expect(screen.getByRole("link", { name: "Settings" })).toHaveAttribute("href", "/settings");
    for (const link of screen.getAllByRole("link", { name: /Shop/ })) {
      expect(link).toHaveAttribute("href", "/server/s1/app/a1");
    }
  });

  it("points the health strip's Monitoring link into the active server", () => {
    renderAt("/server/s1");
    const strip = screen.getByRole("region", { name: "Server health" });
    expect(within(strip).getByRole("link", { name: "Monitoring" })).toHaveAttribute("href", "/server/s1/monitoring");
  });

  it("switches servers from the switcher", async () => {
    const user = userEvent.setup();
    renderAt("/server/s1");
    await user.click(screen.getAllByRole("button", { name: /Alpha/ })[0]);
    await user.click(within(await screen.findByRole("menu")).getByText("Beta"));
    expect(selectServer).toHaveBeenLastCalledWith("s2");
  });

  it("opens the create page of the active server", async () => {
    const user = userEvent.setup();
    renderAt("/server/s1");
    fakeAPI({ "/api/v1/template/get-templates": { templates: [] } });
    await user.click(within(screen.getByRole("main")).getByRole("button", { name: "New app" }));
    await user.click(await screen.findByRole("link", { name: /Compose file/ }));
    expect(await screen.findByText("create page")).toBeInTheDocument();
  });

  it("shows the bare dashboard at / while the org has no server", () => {
    servers = [];
    renderAt("/");
    expect(screen.getByText("no servers")).toBeInTheDocument();
  });
});
