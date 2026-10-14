import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import { AppSidebar } from "@/components/app-sidebar";
import { NewAppDialogProvider } from "@/components/new-app/new-app-dialog-provider";
import { SidebarProvider } from "@/components/ui/sidebar";
import { TooltipProvider } from "@/components/ui/tooltip";
import { SidebarContext, useSidebar, type SidebarContextProps } from "@/components/ui/use-sidebar";
import { CommandPaletteProvider } from "@/context/palette-context";
import { useCommandPalette } from "@/context/use-palette";
import { stubViewport } from "@/test/media";

const ALPHA = { id: "s1", organizationId: "o", name: "Alpha", createdAt: "", lastSeenAt: null, capabilities: {} };
let activeServerId: string | null = "s1";
let unread = 0;

vi.mock("@/context/use-servers", () => ({
  useServers: () => ({
    servers: activeServerId ? [ALPHA] : [],
    activeServer: activeServerId ? ALPHA : null,
    activeServerId,
    selectServer: vi.fn(),
    statusByServer: {},
    loading: false,
    refresh: vi.fn(),
  }),
}));
vi.mock("@/context/use-apps", () => ({
  useApps: () => ({
    apps: [
      { id: "a1", serverId: "s1", name: "Shop", version: "1", icon: "", color: "", createdAt: "" },
      { id: "a2", serverId: "s2", name: "Elsewhere", version: "1", icon: "", color: "", createdAt: "" },
    ],
    statusByApp: { a1: 1 },
    loading: false,
  }),
}));
vi.mock("@/hooks/use-inbox-unread", () => ({
  useInboxUnread: () => ({ count: unread, seenAt: 0, isUnread: () => false, markAllRead: () => {} }),
}));
// New app's chooser counts the templates on its first open.
vi.mock("@/hooks/use-template-count", () => ({ useTemplateCount: () => 1 }));
// NavUser needs the account contexts; it has its own test.
vi.mock("@/components/nav-user", () => ({ NavUser: () => null }));

const setOpenMobile = vi.fn();
const toggleSidebar = vi.fn();
function sidebarState(isMobile: boolean, state: "expanded" | "collapsed" = "expanded"): SidebarContextProps {
  return {
    state,
    open: true,
    setOpen: () => {},
    openMobile: isMobile,
    setOpenMobile,
    isMobile,
    toggleSidebar,
  };
}

function PaletteState() {
  return <span data-testid="palette">{useCommandPalette().open ? "open" : "closed"}</span>;
}

function renderAt(url: string, isMobile = false, state: "expanded" | "collapsed" = "expanded") {
  return render(
    <MemoryRouter initialEntries={[url]}>
      <CommandPaletteProvider>
        <TooltipProvider>
          <SidebarContext.Provider value={sidebarState(isMobile, state)}>
            <NewAppDialogProvider>
              <AppSidebar />
              <PaletteState />
            </NewAppDialogProvider>
          </SidebarContext.Provider>
        </TooltipProvider>
      </CommandPaletteProvider>
    </MemoryRouter>,
  );
}

const lit = () =>
  screen
    .getAllByRole("link")
    .filter((a) => a.getAttribute("data-active") === "true")
    .map((a) => a.getAttribute("href"));

describe("AppSidebar", () => {
  afterEach(() => {
    activeServerId = "s1";
    unread = 0;
    setOpenMobile.mockClear();
    toggleSidebar.mockClear();
  });

  it("has a visible control that collapses and expands the sidebar", async () => {
    const user = userEvent.setup();
    const { unmount } = renderAt("/server/s1");
    await user.click(screen.getByRole("button", { name: "Collapse sidebar" }));
    expect(toggleSidebar).toHaveBeenCalledTimes(1);
    unmount();
    renderAt("/server/s1", false, "collapsed");
    expect(screen.getByRole("button", { name: "Expand sidebar" })).toBeInTheDocument();
  });

  it("lists Apps with the active server's apps and New app, then Monitoring, Inbox, Templates, and Settings", () => {
    renderAt("/server/s1");
    expect(screen.getAllByRole("link").map((a) => a.getAttribute("href"))).toEqual([
      "/server/s1",
      "/server/s1/app/a1",
      "/server/s1/monitoring",
      "/inbox",
      "/templates",
      "/settings",
    ]);
    expect(screen.getByRole("link", { name: "Shop, Running" })).toBeInTheDocument();
    expect(screen.queryByText("Elsewhere")).not.toBeInTheDocument();
    expect(screen.queryByText("Dashboard")).not.toBeInTheDocument();
  });

  it.each([
    ["/server/s1", ["/server/s1"]],
    ["/server/s1/app/a1", ["/server/s1/app/a1"]],
    // New app is a button that opens the chooser: no entry for the page.
    ["/server/s1/new", []],
    ["/server/s1/monitoring", ["/server/s1/monitoring"]],
    ["/inbox", ["/inbox"]],
    ["/templates/t1", ["/templates"]],
    ["/settings/members", ["/settings"]],
    ["/server/s1/settings/docker", ["/settings"]],
  ])("lights only the current entry at %s", (url, expected) => {
    renderAt(url);
    expect(lit()).toEqual(expected);
  });

  it("counts unread Inbox items, up to 99+, and says so inside the link", () => {
    unread = 3;
    const { unmount } = renderAt("/server/s1");
    expect(screen.getByRole("link", { name: "Inbox, 3 unread" })).toBeInTheDocument();
    expect(screen.getByText("3")).toBeInTheDocument();
    unmount();
    unread = 150;
    renderAt("/server/s1");
    expect(screen.getByRole("link", { name: "Inbox, 150 unread" })).toBeInTheDocument();
    expect(screen.getByText("99+")).toBeInTheDocument();
  });

  it("marks the Inbox icon with a dot that shows when the sidebar is collapsed to icons", () => {
    unread = 3;
    renderAt("/server/s1");
    const dot = screen.getByTestId("inbox-dot");
    expect(dot).toHaveClass("group-data-[collapsible=icon]:block");
    expect(screen.getByRole("link", { name: /Inbox/ })).toContainElement(dot);
  });

  it("shows no badge when nothing is unread", () => {
    renderAt("/server/s1");
    expect(screen.getByRole("link", { name: "Inbox" })).toBeInTheDocument();
    expect(screen.queryByText(/unread/)).not.toBeInTheDocument();
    expect(screen.queryByTestId("inbox-dot")).not.toBeInTheDocument();
  });

  it("opens the palette from the search box", async () => {
    renderAt("/server/s1");
    await userEvent.setup().click(screen.getByRole("button", { name: /Search/ }));
    expect(screen.getByTestId("palette")).toHaveTextContent("open");
  });

  it("has no server pages before there is a server", () => {
    activeServerId = null;
    renderAt("/");
    expect(screen.getByRole("link", { name: "Apps" })).toHaveAttribute("href", "/");
    expect(screen.queryByRole("link", { name: "Monitoring" })).not.toBeInTheDocument();
    expect(screen.queryByRole("link", { name: /New app/ })).not.toBeInTheDocument();
  });

  it("opens the New app chooser from the sidebar, closing the phone sheet first", async () => {
    renderAt("/server/s1", true);
    await userEvent.setup().click(screen.getByRole("button", { name: "New app" }));
    expect(setOpenMobile).toHaveBeenCalledWith(false);
    const dialog = await screen.findByRole("dialog", { name: "New app" });
    expect(dialog).toHaveTextContent("How do you want to start?");
    expect(screen.getByRole("link", { name: /Compose file/ })).toHaveAttribute("href", "/server/s1/new?from=compose");
  });

  it("keeps the New app chooser open on a phone once the sheet holding the button closes", async () => {
    stubViewport(375);
    const user = userEvent.setup();
    function OpenMenu() {
      const { setOpenMobile: open } = useSidebar();
      return <button onClick={() => open(true)}>Open menu</button>;
    }
    render(
      <MemoryRouter initialEntries={["/server/s1"]}>
        <CommandPaletteProvider>
          <TooltipProvider>
            <SidebarProvider>
              <NewAppDialogProvider>
                <OpenMenu />
                <AppSidebar />
              </NewAppDialogProvider>
            </SidebarProvider>
          </TooltipProvider>
        </CommandPaletteProvider>
      </MemoryRouter>,
    );
    await user.click(screen.getByRole("button", { name: "Open menu" }));
    await user.click(await screen.findByRole("button", { name: "New app" }));
    // The sheet (and the button in it) is gone; the chooser stays.
    await waitFor(() => expect(screen.queryByRole("button", { name: "New app" })).not.toBeInTheDocument());
    const dialog = await screen.findByRole("dialog", { name: "New app" });
    expect(dialog).toHaveTextContent("How do you want to start?");
    expect(within(dialog).getByRole("link", { name: /Compose file/ })).toHaveAttribute("href", "/server/s1/new?from=compose");
  });

  it("offers no New app outside the shell's chooser", () => {
    render(
      <MemoryRouter initialEntries={["/server/s1"]}>
        <CommandPaletteProvider>
          <TooltipProvider>
            <SidebarContext.Provider value={sidebarState(false)}>
              <AppSidebar />
            </SidebarContext.Provider>
          </TooltipProvider>
        </CommandPaletteProvider>
      </MemoryRouter>,
    );
    expect(screen.queryByRole("button", { name: "New app" })).not.toBeInTheDocument();
  });

  it("closes the phone sheet when a link is followed", async () => {
    renderAt("/server/s1", true);
    await userEvent.setup().click(screen.getByRole("link", { name: "Templates" }));
    expect(setOpenMobile).toHaveBeenCalledWith(false);
  });
});
