import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { NotificationsContext, type NotificationsContextValue } from "@/context/notifications-context-base";
import HomePage from "@/pages/home";
import { fakeAPI } from "@/test/fake-api";

const homelab = { id: "s1", organizationId: "o", name: "homelab", createdAt: "", lastSeenAt: null, capabilities: {} };
let servers: { activeServer: typeof homelab | null; loading: boolean; refresh: () => Promise<void> };
vi.mock("@/context/use-servers", () => ({ useServers: () => servers }));
let appsState: { apps: { id: string }[]; loading: boolean };
let statusState: Record<string, number> = {};
vi.mock("@/context/use-apps", () => ({
  useApps: () => ({ ...appsState, statusByApp: statusState }),
}));
vi.mock("@/components/server-health-strip", () => ({ ServerHealthStrip: () => <div data-testid="strip" /> }));
vi.mock("@/components/alerts/active-alerts", () => ({ FiringAlertsLine: () => <div data-testid="firing" /> }));
vi.mock("@/components/app-grid", () => ({ AppGrid: () => <div data-testid="grid" /> }));
vi.mock("@/components/server-creation-dialog", () => ({
  ServerSelectionDialog: ({ isOpen }: { isOpen: boolean }) => (isOpen ? <p>add server dialog</p> : null),
}));

// New app opens the chooser, which reads the organization's templates.
const notifications = { connected: true, subscribe: () => () => {}, waitFor: vi.fn() } as NotificationsContextValue;

function renderHome() {
  return render(
    <MemoryRouter initialEntries={["/server/s1"]}>
      <NotificationsContext.Provider value={notifications}>
        <Routes>
          <Route path="/server/s1" element={<HomePage />} />
          <Route path="/server/s1/new" element={<p>new app page</p>} />
        </Routes>
      </NotificationsContext.Provider>
    </MemoryRouter>,
  );
}

describe("HomePage", () => {
  beforeEach(() => {
    servers = { activeServer: homelab, loading: false, refresh: vi.fn(async () => {}) };
    appsState = { apps: [{ id: "a1" }, { id: "a2" }, { id: "a3" }], loading: false };
    statusState = { a1: 1, a2: 1, a3: 5 };
    fakeAPI({ "/api/v1/template/get-templates": { templates: [] } });
  });

  afterEach(() => vi.unstubAllGlobals());

  it("names just the server until the first status report, not '0 running'", () => {
    statusState = {};
    renderHome();
    expect(screen.getByText("homelab")).toBeInTheDocument();
    expect(screen.queryByText(/running/)).not.toBeInTheDocument();
  });

  it("names just the server until the first list of apps has arrived", () => {
    appsState = { apps: [], loading: true };
    renderHome();
    expect(screen.getByText("homelab")).toBeInTheDocument();
    expect(screen.queryByText(/no apps yet/)).not.toBeInTheDocument();
  });

  it("says there are no apps once the list has loaded empty", () => {
    appsState = { apps: [], loading: false };
    renderHome();
    expect(screen.getByText("homelab · no apps yet")).toBeInTheDocument();
  });

  it("is the Apps page: counts under the title, one New app, health and alerts above the apps", () => {
    renderHome();
    expect(screen.getByRole("heading", { level: 1, name: "Apps" })).toBeInTheDocument();
    expect(screen.getByText("homelab · 2 running · 1 stopped")).toBeInTheDocument();
    // One primary action: New app, a button that opens the chooser.
    expect(screen.getAllByRole("button")).toHaveLength(1);
    expect(screen.getByRole("button", { name: "New app" })).toBeInTheDocument();
    const strip = screen.getByTestId("strip");
    const grid = screen.getByTestId("grid");
    expect(strip.compareDocumentPosition(screen.getByTestId("firing")) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(strip.compareDocumentPosition(grid) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    // The multi-server cards are gone from this page.
    expect(screen.queryByRole("region", { name: "Servers" })).not.toBeInTheDocument();
  });

  it("opens the ways to start from New app", async () => {
    const user = userEvent.setup();
    renderHome();
    await user.click(screen.getByRole("button", { name: "New app" }));
    const dialog = await screen.findByRole("dialog", { name: "New app" });
    const compose = within(dialog).getByRole("link", { name: /Compose file/ });
    expect(compose).toHaveAttribute("href", "/server/s1/new?from=compose");
    await user.click(compose);
    expect(await screen.findByText("new app page")).toBeInTheDocument();
  });

  it("asks for a server when there is none", async () => {
    const user = userEvent.setup();
    servers = { ...servers, activeServer: null };
    renderHome();
    expect(screen.getByText("Add a server to run your apps on.")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "New app" })).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Add server" }));
    expect(screen.getByText("add server dialog")).toBeInTheDocument();
  });
});
