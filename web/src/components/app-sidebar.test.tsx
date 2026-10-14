import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import { AppSidebar } from "@/components/app-sidebar";
import { TooltipProvider } from "@/components/ui/tooltip";
import { SidebarContext, type SidebarContextProps } from "@/components/ui/use-sidebar";
import { CommandPaletteProvider } from "@/context/palette-context";
import { useCommandPalette } from "@/context/use-palette";

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
// NavUser needs the account contexts; it has its own test.
vi.mock("@/components/nav-user", () => ({ NavUser: () => null }));

const setOpenMobile = vi.fn();
function sidebarState(isMobile: boolean): SidebarContextProps {
  return {
    state: "expanded",
    open: true,
    setOpen: () => {},
    openMobile: isMobile,
    setOpenMobile,
    isMobile,
    toggleSidebar: () => {},
  };
}

function PaletteState() {
  return <span data-testid="palette">{useCommandPalette().open ? "open" : "closed"}</span>;
}

function renderAt(url: string, isMobile = false) {
  return render(
    <MemoryRouter initialEntries={[url]}>
      <CommandPaletteProvider>
        <TooltipProvider>
          <SidebarContext.Provider value={sidebarState(isMobile)}>
            <AppSidebar />
            <PaletteState />
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
  });

  it("lists Apps with the active server's apps and New app, then Monitoring, Inbox, Templates, and Settings", () => {
    renderAt("/server/s1");
    expect(screen.getAllByRole("link").map((a) => a.getAttribute("href"))).toEqual([
      "/server/s1",
      "/server/s1/app/a1",
      "/server/s1/new",
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
    ["/server/s1/new", ["/server/s1/new"]],
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

  it("closes the phone sheet when a link is followed", async () => {
    renderAt("/server/s1", true);
    await userEvent.setup().click(screen.getByRole("link", { name: "Templates" }));
    expect(setOpenMobile).toHaveBeenCalledWith(false);
  });
});
