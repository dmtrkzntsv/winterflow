import { act, render, screen } from "@testing-library/react";
import { MemoryRouter, Outlet, Route, Routes, useLocation, useOutletContext } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { NotFound } from "@/components/not-found";
import { DefaultServer, ServerScope } from "@/components/server-routes";
import { ServersProvider } from "@/context/servers-context";
import { useServers } from "@/context/use-servers";

vi.mock("@/context/use-auth", () => ({ useAuth: () => ({ isAuthenticated: true }) }));
vi.mock("@/context/use-notifications", () => ({
  useNotifications: () => ({ subscribe: () => () => {}, connected: true }),
}));

const SERVERS = [
  { id: "s1", organization_id: "o", name: "Alpha", created_at: "", last_seen_at: null },
  { id: "s2", organization_id: "o", name: "Beta", created_at: "", last_seen_at: null },
];

beforeEach(() => {
  vi.stubGlobal(
    "fetch",
    vi.fn((url: string) =>
      Promise.resolve(
        new Response(
          JSON.stringify(
            url.includes("get-servers-status")
              ? { data: { servers: [] } }
              : { success: true, data: { servers: SERVERS } },
          ),
        ),
      ),
    ),
  );
});
afterEach(() => vi.unstubAllGlobals());

// Probe shows where the tab is and which server it is scoped to, and offers
// the switcher's action.
function Probe() {
  const { pathname, search } = useLocation();
  const { activeServer, selectServer } = useServers();
  return (
    <div>
      <span data-testid="at">{pathname + search}</span>
      <span data-testid="server">{activeServer?.name ?? "-"}</span>
      <button onClick={() => selectServer("s2")}>switch</button>
    </div>
  );
}

function renderAt(url: string) {
  return render(
    <MemoryRouter initialEntries={[url]}>
      <ServersProvider>
        <Routes>
          <Route path="/" element={<DefaultServer fallback={<Probe />} />} />
          <Route path="/server/:serverId" element={<ServerScope />}>
            <Route index element={<Probe />} />
            <Route path="monitoring" element={<Probe />} />
            <Route path="settings/:section" element={<Probe />} />
            <Route path="new" element={<Probe />} />
            <Route path="app/:appId" element={<Probe />} />
          </Route>
          <Route path="/inbox" element={<Probe />} />
          <Route path="/templates" element={<Probe />} />
          <Route path="/settings/members" element={<Probe />} />
          <Route path="/user/tokens" element={<Probe />} />
          <Route path="*" element={<NotFound />} />
        </Routes>
      </ServersProvider>
    </MemoryRouter>,
  );
}

const at = () => screen.getByTestId("at").textContent;
const server = () => screen.getByTestId("server").textContent;

describe("server-scoped URLs", () => {
  it("takes the server from the URL, not the first one", async () => {
    renderAt("/server/s2/app/a9");
    expect(await screen.findByText("Beta")).toBeInTheDocument();
    expect(at()).toBe("/server/s2/app/a9");
  });

  it("opens the default server's dashboard at /", async () => {
    renderAt("/");
    await screen.findByTestId("at");
    expect(at()).toBe("/server/s1");
    expect(server()).toBe("Alpha");
  });

  it("shows an error page for an unknown server id", async () => {
    renderAt("/server/nope/settings/general");
    expect(await screen.findByText("Server not found")).toBeInTheDocument();
    expect(screen.queryByTestId("at")).not.toBeInTheDocument();
  });

  it("shows an error page for unscoped and unknown routes", async () => {
    renderAt("/settings");
    expect(await screen.findByText("Page not found")).toBeInTheDocument();
  });

  it("opens the new server's General page when switching from a server's settings", async () => {
    renderAt("/server/s1/settings/domains?x=1");
    await screen.findByText("Alpha");
    act(() => screen.getByText("switch").click());
    expect(at()).toBe("/server/s2/settings/general");
    expect(server()).toBe("Beta");
  });

  it("opens the server's General page when switching from the organization's settings", async () => {
    renderAt("/settings/members");
    await screen.findByText("Alpha");
    act(() => screen.getByText("switch").click());
    expect(at()).toBe("/server/s2/settings/general");
  });

  it("lands on the new server's Apps when switching from an app or the new-app page", async () => {
    renderAt("/server/s1/app/a1");
    await screen.findByText("Alpha");
    act(() => screen.getByText("switch").click());
    expect(at()).toBe("/server/s2");
  });

  it("lands on the new server's Apps when switching from the new-app page", async () => {
    renderAt("/server/s1/new?from=git");
    await screen.findByText("Alpha");
    act(() => screen.getByText("switch").click());
    expect(at()).toBe("/server/s2");
  });

  it.each(["/inbox", "/templates"])("stays on %s (organization-wide) and only changes the active server", async (url) => {
    renderAt(url);
    await screen.findByText("Alpha");
    act(() => screen.getByText("switch").click());
    expect(at()).toBe(url);
    expect(server()).toBe("Beta");
  });

  it("scopes monitoring to the URL's server and keeps it when switching", async () => {
    renderAt("/server/s1/monitoring");
    await screen.findByText("Alpha");
    act(() => screen.getByText("switch").click());
    expect(at()).toBe("/server/s2/monitoring");
    expect(server()).toBe("Beta");
  });

  it("shows an error page for monitoring of an unknown server", async () => {
    renderAt("/server/nope/monitoring");
    expect(await screen.findByText("Server not found")).toBeInTheDocument();
  });

  it("has no unscoped /monitoring", async () => {
    renderAt("/monitoring");
    expect(await screen.findByText("Page not found")).toBeInTheDocument();
  });

  it("passes the layout's outlet context through to the server's pages", async () => {
    function Layout() {
      return <Outlet context={{ marker: "layout" }} />;
    }
    function Page() {
      const ctx = useOutletContext<{ marker?: string } | undefined>();
      return <span>context: {ctx?.marker ?? "none"}</span>;
    }
    render(
      <MemoryRouter initialEntries={["/server/s1"]}>
        <ServersProvider>
          <Routes>
            <Route element={<Layout />}>
              <Route path="/server/:serverId" element={<ServerScope />}>
                <Route index element={<Page />} />
              </Route>
            </Route>
          </Routes>
        </ServersProvider>
      </MemoryRouter>,
    );
    expect(await screen.findByText("context: layout")).toBeInTheDocument();
  });

  it("falls back to the first server outside a server's scope", async () => {
    renderAt("/user/tokens");
    expect(await screen.findByText("Alpha")).toBeInTheDocument();
  });
});
