import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Outlet, Route, Routes } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";

import { AppSidebar } from "@/components/app-sidebar";
import { DefaultServer } from "@/components/server-routes";
import { TooltipProvider } from "@/components/ui/tooltip";
import { SidebarContext, type SidebarContextProps } from "@/components/ui/use-sidebar";
import HomePage from "@/pages/home";

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

function renderAt(url: string) {
  return render(
    <MemoryRouter initialEntries={[url]}>
      <TooltipProvider>
        <SidebarContext.Provider value={sidebar}>
          <AppSidebar />
          <Routes>
            <Route element={<Outlet context={{ setBreadcrumbs: () => {} }} />}>
              <Route path="/" element={<DefaultServer fallback={<p>no servers</p>} />} />
              <Route path="/server/s1" element={<HomePage />} />
              <Route path="/server/s1/create-app" element={<p>create page</p>} />
              <Route path="/server/s2/monitoring" element={<p>beta monitoring</p>} />
            </Route>
          </Routes>
        </SidebarContext.Provider>
      </TooltipProvider>
    </MemoryRouter>,
  );
}

describe("server-scoped links", () => {
  it("points the sidebar and the app grid into the active server", async () => {
    renderAt("/server/s1");
    expect(screen.getByRole("link", { name: "Dashboard" })).toHaveAttribute("href", "/server/s1");
    expect(screen.getByRole("link", { name: "Monitoring" })).toHaveAttribute(
      "href",
      "/server/s1/monitoring",
    );
    expect(screen.getByRole("link", { name: "Server Settings" })).toHaveAttribute(
      "href",
      "/server/s1/settings",
    );
    for (const link of screen.getAllByRole("link", { name: /Shop/ })) {
      expect(link).toHaveAttribute("href", "/server/s1/app/a1");
    }
  });

  it("opens a server card's monitoring in that server's scope", async () => {
    const user = userEvent.setup();
    renderAt("/server/s1");
    await user.click(screen.getByTitle(/^Beta:/));
    expect(await screen.findByText("beta monitoring")).toBeInTheDocument();
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
    await user.click(screen.getByRole("button", { name: "Add app" }));
    expect(await screen.findByText("create page")).toBeInTheDocument();
  });

  it("shows the bare dashboard at / while the org has no server", () => {
    servers = [];
    renderAt("/");
    expect(screen.getByText("no servers")).toBeInTheDocument();
  });
});
