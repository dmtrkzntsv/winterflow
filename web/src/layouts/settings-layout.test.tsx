import { render, screen, within } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import { SettingsIndex, SettingsLayout } from "@/layouts/settings-layout";
import { stubViewport } from "@/test/media";

type FakeServer = { id: string; name: string; features?: Record<string, boolean> };
let active: FakeServer | null = { id: "s1", name: "Alpha", features: { ingress: true } };
let email = "ann@example.test";
let mustChange = false;
let loaded = true;

vi.mock("@/context/use-servers", () => ({
  useServers: () => ({
    activeServer: active,
    activeServerId: loaded ? (active?.id ?? null) : null,
    servers: active && loaded ? [active] : [],
    loading: false,
    loaded,
  }),
}));
vi.mock("@/context/use-profile", () => ({
  useProfile: () => ({ profile: { user_id: "u1", name: "Ann", email, must_change_password: mustChange } }),
}));

function Where() {
  return <span data-testid="at">{useLocation().pathname}</span>;
}

function renderAt(url: string) {
  return render(
    <MemoryRouter initialEntries={[url]}>
      <Routes>
        <Route path="/settings" element={<SettingsLayout />}>
          <Route index element={<SettingsIndex />} />
          <Route path="organization" element={<p>organization body</p>} />
          <Route path="members" element={<p>members body</p>} />
          <Route path="password" element={<p>password body</p>} />
        </Route>
        <Route path="/server/:serverId/settings" element={<SettingsLayout />}>
          <Route path="general" element={<p>general body</p>} />
        </Route>
      </Routes>
      <Where />
    </MemoryRouter>,
  );
}

describe("SettingsLayout", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    active = { id: "s1", name: "Alpha", features: { ingress: true } };
    email = "ann@example.test";
    mustChange = false;
    loaded = true;
  });

  it("puts the sub-menu beside the page on a desktop", () => {
    renderAt("/settings/members");
    const menu = screen.getByRole("navigation", { name: "Settings" });
    expect(within(menu).getByText("Server · Alpha")).toBeInTheDocument();
    expect(within(menu).getByText("Organization")).toBeInTheDocument();
    expect(within(menu).getByText("Account")).toBeInTheDocument();
    expect(within(menu).getByRole("link", { name: "Members" })).toHaveAttribute("aria-current", "page");
    expect(within(menu).getByRole("link", { name: "Domains" })).toHaveAttribute("href", "/server/s1/settings/domains");
    expect(screen.getByText("members body")).toBeInTheDocument();
  });

  it("leaves Domains out on a server without ingress and Password out without local credentials", () => {
    active = { id: "s1", name: "Alpha", features: {} };
    email = "";
    renderAt("/settings/members");
    const menu = screen.getByRole("navigation", { name: "Settings" });
    expect(within(menu).queryByRole("link", { name: "Domains" })).not.toBeInTheDocument();
    expect(within(menu).queryByRole("link", { name: "Password" })).not.toBeInTheDocument();
    expect(within(menu).getByRole("link", { name: "API tokens" })).toBeInTheDocument();
  });

  it("opens the active server's General page at /settings on a desktop", async () => {
    renderAt("/settings");
    expect(await screen.findByText("general body")).toBeInTheDocument();
    expect(screen.getByTestId("at")).toHaveTextContent("/server/s1/settings/general");
  });

  it("waits for the server list on a hard load instead of guessing Organization", async () => {
    loaded = false;
    const { rerender } = renderAt("/settings");
    expect(screen.getByTestId("at")).toHaveTextContent("/settings");
    loaded = true;
    rerender(
      <MemoryRouter initialEntries={["/settings"]}>
        <Routes>
          <Route path="/settings" element={<SettingsLayout />}>
            <Route index element={<SettingsIndex />} />
            <Route path="organization" element={<p>organization body</p>} />
          </Route>
          <Route path="/server/:serverId/settings" element={<SettingsLayout />}>
            <Route path="general" element={<p>general body</p>} />
          </Route>
        </Routes>
        <Where />
      </MemoryRouter>,
    );
    expect(await screen.findByText("general body")).toBeInTheDocument();
  });

  it("opens Organization at /settings when there is no server", async () => {
    active = null;
    renderAt("/settings");
    expect(await screen.findByText("organization body")).toBeInTheDocument();
  });

  it("is a list to drill into at /settings on a phone", () => {
    stubViewport(375);
    renderAt("/settings");
    expect(screen.getByRole("heading", { level: 1, name: "Settings" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Members" })).toHaveAttribute("href", "/settings/members");
    expect(screen.getByTestId("at")).toHaveTextContent(/^\/settings$/);
  });

  it("shows a sub-page alone with a way back on a phone", () => {
    stubViewport(375);
    renderAt("/settings/members");
    expect(screen.getByText("members body")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Settings" })).toHaveAttribute("href", "/settings");
    expect(screen.queryByRole("navigation", { name: "Settings" })).not.toBeInTheDocument();
  });

  it.each([false, true])("shows only the Password page, without a menu or a way back, to a user who must change the password (phone: %s)", (phone) => {
    mustChange = true;
    if (phone) stubViewport(375);
    renderAt("/settings/password");
    expect(screen.getByText("password body")).toBeInTheDocument();
    expect(screen.queryByRole("navigation", { name: "Settings" })).not.toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "Settings" })).not.toBeInTheDocument();
  });
});
