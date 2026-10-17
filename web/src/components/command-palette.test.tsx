import { act, fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { MemoryRouter, useLocation } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import { CommandPalette } from "@/components/command-palette";
import { CommandPaletteProvider } from "@/context/palette-context";
import { usePaletteActions } from "@/context/use-palette";
import { RECENT_KEY, readRecent } from "@/lib/palette";
import { setThemePreference } from "@/lib/theme";

const SERVERS = [
  { id: "s1", organizationId: "o", name: "Alpha", createdAt: "", lastSeenAt: null, capabilities: {}, features: { ingress: true } },
  { id: "s2", organizationId: "o", name: "Beta", createdAt: "", lastSeenAt: null, capabilities: {} },
];
const selectServer = vi.fn();
const templatesApi = { listTemplates: async () => [], getRevision: async () => ({ body: { icon: "", color: "", summary: "" } }) };
vi.mock("@/context/use-servers", () => ({
  useServers: () => ({
    servers: SERVERS,
    activeServer: SERVERS[0],
    activeServerId: "s1",
    selectServer,
    statusByServer: {},
    loading: false,
  }),
}));
const SHOP = { id: "a1", serverId: "s1", name: "Shop", version: "1", icon: "", color: "", createdAt: "" };
let apps = [SHOP];
vi.mock("@/context/use-apps", () => ({ useApps: () => ({ apps, statusByApp: { a1: 1 }, deployFailures: {} }) }));
vi.mock("@/hooks/use-templates-api", () => ({ useTemplatesApi: () => templatesApi }));
vi.mock("@/context/use-profile", () => ({
  useProfile: () => ({ profile: { user_id: "u1", name: "Ann", email: "ann@example.test", must_change_password: false } }),
}));

function Where() {
  const { pathname, search } = useLocation();
  return <span data-testid="at">{pathname + search}</span>;
}

function renderPalette({ onAddServer, children }: { onAddServer?: () => void; children?: ReactNode } = {}) {
  return render(
    <MemoryRouter initialEntries={["/server/s1"]}>
      <CommandPaletteProvider>
        <CommandPalette onAddServer={onAddServer} />
        {children}
        <Where />
      </CommandPaletteProvider>
    </MemoryRouter>,
  );
}

const openWithKeyboard = () => act(() => void fireEvent.keyDown(window, { key: "k", ctrlKey: true }));
const input = () => screen.findByPlaceholderText("Search apps, pages and actions…");

describe("CommandPalette", () => {
  afterEach(() => {
    selectServer.mockClear();
    apps = [SHOP];
    localStorage.clear();
    act(() => setThemePreference("system"));
    document.documentElement.classList.remove("dark");
  });

  it("opens on Ctrl+K and jumps to a page", async () => {
    const user = userEvent.setup();
    renderPalette();
    openWithKeyboard();
    await user.type(await input(), "monitor");
    await user.click(screen.getByRole("option", { name: /Monitoring/ }));
    expect(screen.getByTestId("at")).toHaveTextContent("/server/s1/monitoring");
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(readRecent()[0]).toMatchObject({ id: "page:monitoring", to: "/server/s1/monitoring" });
  });

  it("leads apps with their status dot and the rest with an icon, and marks the highlighted item's Enter", async () => {
    renderPalette();
    openWithKeyboard();
    await input();
    const shop = screen.getByRole("option", { name: /Shop/ });
    expect(within(shop).getByTestId("palette-app-status").querySelector("[data-tone]")).toHaveAttribute("data-tone", "ok");
    expect(screen.getByRole("option", { name: /Monitoring/ }).querySelector("svg")).not.toBeNull();
    // Every item carries the ↵, shown only while it is the highlighted one.
    expect(within(shop).getByText("↵")).toHaveClass("hidden", "in-data-[selected=true]:inline");
  });

  it("finds this server's apps, the Settings sections and the other servers", async () => {
    const user = userEvent.setup();
    renderPalette();
    openWithKeyboard();
    await user.type(await input(), "shop");
    await user.click(screen.getByRole("option", { name: /Shop/ }));
    expect(screen.getByTestId("at")).toHaveTextContent("/server/s1/app/a1");

    openWithKeyboard();
    await user.type(await input(), "domains");
    await user.click(screen.getByRole("option", { name: /Domains/ }));
    expect(screen.getByTestId("at")).toHaveTextContent("/server/s1/settings/domains");

    openWithKeyboard();
    await user.type(await input(), "beta");
    await user.click(screen.getByRole("option", { name: /Beta/ }));
    // Through selectServer, which keeps the page kind; the palette doesn't navigate itself.
    expect(selectServer).toHaveBeenCalledWith("s2");
    expect(screen.getByTestId("at")).toHaveTextContent("/server/s1");
    expect(readRecent()[0]).toMatchObject({ id: "server:s2" });
  });

  it("searches names, hints and keywords, not ids or paths", async () => {
    apps = [{ ...SHOP, id: "a-deadbeef-cafe", name: "Website" }];
    const user = userEvent.setup();
    renderPalette();
    openWithKeyboard();
    const box = await input();
    await user.type(box, "dead");
    expect(await screen.findByText("Nothing matches.")).toBeInTheDocument();
    expect(screen.queryByRole("option")).not.toBeInTheDocument();
    await user.clear(box);
    await user.type(box, "logs");
    expect(screen.queryByRole("option", { name: /Storages/ })).not.toBeInTheDocument();
    await user.clear(box);
    await user.type(box, "Web");
    expect(screen.getByRole("option", { name: /Website/ })).toBeInTheDocument();
  });

  it("reaches the second of two same-named apps with the keyboard", async () => {
    apps = [SHOP, { ...SHOP, id: "a2" }];
    const user = userEvent.setup();
    renderPalette();
    openWithKeyboard();
    await user.type(await input(), "shop");
    expect(screen.getAllByRole("option", { name: /Shop/ })).toHaveLength(2);
    await user.keyboard("{ArrowDown}{Enter}");
    expect(screen.getByTestId("at")).toHaveTextContent("/server/s1/app/a2");
  });

  it("shows recent items for an empty query, without the ones that are gone", async () => {
    localStorage.setItem(
      RECENT_KEY,
      JSON.stringify([
        { id: "app:gone", label: "Gone", to: "/server/s1/app/gone" },
        { id: "page:inbox", label: "Inbox", to: "/inbox" },
      ]),
    );
    const user = userEvent.setup();
    renderPalette();
    openWithKeyboard();
    const recent = await screen.findByRole("group", { name: "Recent" });
    expect(within(recent).getByRole("option", { name: /Inbox/ })).toBeInTheDocument();
    expect(within(recent).queryByText("Gone")).not.toBeInTheDocument();
    await user.type(await input(), "in");
    expect(screen.queryByRole("group", { name: "Recent" })).not.toBeInTheDocument();
  });

  it("runs the actions the open page registered", async () => {
    const restart = vi.fn();
    function AppPage() {
      usePaletteActions("Shop", [{ id: "restart", label: "Restart", run: restart }]);
      return null;
    }
    const user = userEvent.setup();
    renderPalette({ children: <AppPage /> });
    openWithKeyboard();
    const group = await screen.findByRole("group", { name: "Shop" });
    await user.click(within(group).getByRole("option", { name: /Restart/ }));
    expect(restart).toHaveBeenCalled();
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("switches the theme", async () => {
    const user = userEvent.setup();
    renderPalette();
    openWithKeyboard();
    await user.type(await input(), "dark");
    await user.click(screen.getByRole("option", { name: /Theme: Dark/ }));
    expect(document.documentElement).toHaveClass("dark");
  });

  it("opens New app, and offers Add server only where servers can be added", async () => {
    const onAddServer = vi.fn();
    const user = userEvent.setup();
    const { unmount } = renderPalette({ onAddServer });
    openWithKeyboard();
    await user.type(await input(), "add server");
    await user.click(screen.getByRole("option", { name: /Add server/ }));
    expect(onAddServer).toHaveBeenCalled();

    openWithKeyboard();
    await user.type(await input(), "new app");
    await user.click(screen.getByRole("option", { name: /New app/ }));
    expect(screen.getByTestId("at")).toHaveTextContent("/server/s1/new");
    unmount();

    renderPalette();
    openWithKeyboard();
    await input();
    expect(screen.queryByRole("option", { name: /Add server/ })).not.toBeInTheDocument();
  });
});
