import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import { ServerSwitcher } from "@/components/server-switcher";
import { SidebarContext, type SidebarContextProps } from "@/components/ui/use-sidebar";
import type { Server, ServerLiveness } from "@/context/servers-context-base";

let standalone = false;
vi.mock("@/config", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/config")>()),
  get isStandalone() {
    return standalone;
  },
}));

const server = (id: string, name: string, lastSeenAt: string | null = null): Server => ({
  id,
  organizationId: "o",
  name,
  createdAt: "",
  lastSeenAt,
  capabilities: {},
});
const DEFAULT_SERVERS = [
  server("s1", "Alpha", "2026-10-13T10:00:00Z"),
  server("s2", "Beta"),
  server("s3", "Gamma", "2026-10-01T10:00:00Z"),
];
let servers: Server[] = DEFAULT_SERVERS;
let statusByServer: Record<string, ServerLiveness> = { s1: "online", s3: "unknown" };
const selectServer = vi.fn();

vi.mock("@/context/use-servers", () => ({
  useServers: () => ({
    servers,
    activeServer: servers[0] ?? null,
    activeServerId: servers[0]?.id ?? null,
    selectServer,
    statusByServer,
    loading: false,
  }),
}));

const setOpenMobile = vi.fn();

function renderSwitcher(onAddServer = vi.fn(), isMobile = false) {
  const sidebar = { isMobile, state: "expanded", setOpenMobile } as unknown as SidebarContextProps;
  render(
    <SidebarContext.Provider value={sidebar}>
      <ServerSwitcher onAddServer={onAddServer} />
    </SidebarContext.Provider>,
  );
  return onAddServer;
}

describe("ServerSwitcher", () => {
  afterEach(() => {
    standalone = false;
    servers = DEFAULT_SERVERS;
    statusByServer = { s1: "online", s3: "unknown" };
    selectServer.mockClear();
    setOpenMobile.mockClear();
  });

  it("lists the servers with their status and a shortcut each, and switches", async () => {
    const user = userEvent.setup();
    renderSwitcher();
    const trigger = screen.getByRole("button", { name: /Alpha/ });
    expect(trigger).toHaveTextContent("Online");
    await user.click(trigger);
    const menu = await screen.findByRole("menu");
    expect(within(menu).getByRole("img", { name: "Online" })).toBeInTheDocument();
    expect(within(menu).getByRole("img", { name: "Never connected" })).toBeInTheDocument();
    expect(within(menu).getByRole("img", { name: "Offline" })).toBeInTheDocument();
    expect(within(menu).getByLabelText("Current server")).toBeInTheDocument();
    expect(within(menu).getByText("Ctrl+2")).toBeInTheDocument();
    await user.click(within(menu).getByText("Beta"));
    expect(selectServer).toHaveBeenCalledWith("s2");
    expect(setOpenMobile).not.toHaveBeenCalled();
  });

  it("closes the phone sheet when a server is picked", async () => {
    const user = userEvent.setup();
    renderSwitcher(vi.fn(), true);
    await user.click(screen.getByRole("button", { name: /Alpha/ }));
    await user.click(within(await screen.findByRole("menu")).getByText("Beta"));
    expect(selectServer).toHaveBeenCalledWith("s2");
    expect(setOpenMobile).toHaveBeenCalledWith(false);
  });

  it("labels only the first nine servers with a shortcut", async () => {
    servers = Array.from({ length: 10 }, (_, i) => server(`s${i + 1}`, `Server ${i + 1}`));
    const user = userEvent.setup();
    renderSwitcher();
    await user.click(screen.getByRole("button", { name: /Server 1/ }));
    const menu = await screen.findByRole("menu");
    expect(within(menu).getAllByText(/^Ctrl\+\d$/)).toHaveLength(9);
  });

  it("adds a server from the menu", async () => {
    const user = userEvent.setup();
    const onAddServer = renderSwitcher();
    await user.click(screen.getByRole("button", { name: /Alpha/ }));
    await user.click(within(await screen.findByRole("menu")).getByText("Add server"));
    expect(onAddServer).toHaveBeenCalled();
  });

  it("offers adding the first server when there is none", async () => {
    servers = [];
    const user = userEvent.setup();
    const onAddServer = renderSwitcher();
    await user.click(screen.getByRole("button", { name: /Add a server/ }));
    expect(onAddServer).toHaveBeenCalled();
  });

  it("is just the server's name on a standalone install", () => {
    standalone = true;
    renderSwitcher();
    expect(screen.getByText("Alpha")).toBeInTheDocument();
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
    expect(screen.queryByText("Online")).not.toBeInTheDocument();
  });
});
