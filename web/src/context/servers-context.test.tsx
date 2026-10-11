import { act, render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { ServerScope, ToServerScope } from "@/components/server-routes";
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
          <Route path="/" element={<ToServerScope fallback={<Probe />} />} />
          <Route path="/server/:serverId" element={<ServerScope />}>
            <Route index element={<Probe />} />
            <Route path="settings" element={<Probe />} />
            <Route path="app/:appId" element={<Probe />} />
          </Route>
          <Route path="/settings" element={<ToServerScope />} />
          <Route path="/user/tokens" element={<Probe />} />
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

  it("moves / and pre-scoping URLs into the default server", async () => {
    renderAt("/settings?tab=domains");
    await screen.findByTestId("at");
    expect(at()).toBe("/server/s1/settings?tab=domains");
    expect(server()).toBe("Alpha");
  });

  it("sends an unknown server id back to the default server", async () => {
    renderAt("/server/nope/settings");
    await screen.findByTestId("at");
    expect(at()).toBe("/server/s1");
  });

  it("keeps the page when switching servers, except an app page", async () => {
    renderAt("/server/s1/settings?tab=volumes");
    await screen.findByText("Alpha");
    act(() => screen.getByText("switch").click());
    expect(at()).toBe("/server/s2/settings?tab=volumes");
    expect(server()).toBe("Beta");
  });

  it("lands on the new server's dashboard when switching from an app", async () => {
    renderAt("/server/s1/app/a1");
    await screen.findByText("Alpha");
    act(() => screen.getByText("switch").click());
    expect(at()).toBe("/server/s2");
  });

  it("falls back to the first server outside a server's scope", async () => {
    renderAt("/user/tokens");
    expect(await screen.findByText("Alpha")).toBeInTheDocument();
  });
});
