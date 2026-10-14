import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { AppOverview } from "@/components/app-page/app-overview";
import { AppsContext, type App, type AppsContextValue } from "@/context/apps-context-base";
import { forgetRevisions, resetRevisionsCache } from "@/lib/revisions";
import { resetHeldHealth } from "@/lib/backup-activity";
import type { BackupHealth, EffectiveBackup } from "@/lib/backups";

const toast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn(), warning: vi.fn(), info: vi.fn() }));
vi.mock("sonner", () => ({ toast }));
const backups = vi.hoisted(() => ({ runBackup: vi.fn() }));
vi.mock("@/context/use-backups", () => ({ useBackups: () => backups }));
let ingress = true;
vi.mock("@/context/use-servers", () => ({
  useServers: () => ({ activeServerId: "srv-1", activeServer: { id: "srv-1", features: { ingress } } }),
}));
vi.mock("@/components/app-metrics", () => ({
  AppMetricBlocks: () => <div data-testid="kpi-blocks" />,
}));
vi.mock("@/components/restore-points-dialog", () => ({
  RestorePointsDialog: ({ backup }: { backup: { name: string } }) => (
    <div role="dialog" aria-label="Copies">
      copies of {backup.name}
    </div>
  ),
}));

const now = Math.floor(Date.now() / 1000);
const APP: App = { id: "a1", serverId: "srv-1", name: "Shop", version: "3", icon: "", color: "", createdAt: "" };
const LIST: EffectiveBackup[] = [
  { name: "database", type: "sqlite", enabled: true, storage: "disk", every: "1h", in_recipe: true, from_recipe: null },
  { name: "media", type: "files", enabled: false, storage: "", in_recipe: true, from_recipe: null },
  { name: "replica", type: "custom", enabled: true, storage: "s3", in_recipe: true, from_recipe: null },
];
const HEALTH: BackupHealth[] = [
  { name: "database", state: "ok", last_success: now - 600 },
  { name: "replica", state: "ok" },
];
const REVISIONS = {
  current: "dddd",
  deployed: "cccc",
  revisions: [
    { hash: "dddd", subject: "Changed .env", timestamp: now - 60 },
    { hash: "cccc", subject: "Changed compose.yml", timestamp: now - 7200 },
    { hash: "bbbb", subject: "rename to Shop", timestamp: now - 86400 },
    { hash: "aaaa", subject: "create", timestamp: now - 2 * 86400 },
  ],
};

const ctx = {
  getApp: vi.fn(),
  getRevisions: vi.fn(),
  containersByApp: {} as AppsContextValue["containersByApp"],
  backupsByApp: { a1: HEALTH } as AppsContextValue["backupsByApp"],
};

function renderOverview(over: { app?: Partial<App>; status?: number } = {}) {
  const props = { onControl: vi.fn(), onOpenHistory: vi.fn() };
  const view = render(
    <MemoryRouter>
      <AppsContext.Provider value={ctx as unknown as AppsContextValue}>
        <AppOverview app={{ ...APP, ...over.app }} status={over.status ?? 1} pending={null} {...props} />
      </AppsContext.Provider>
    </MemoryRouter>,
  );
  return { ...props, unmount: view.unmount };
}

describe("AppOverview", () => {
  beforeEach(() => {
    resetHeldHealth();
    resetRevisionsCache();
    ingress = true;
    backups.runBackup.mockReset();
    toast.error.mockReset();
    ctx.getApp.mockReset().mockResolvedValue({ backups: LIST });
    ctx.getRevisions.mockReset().mockResolvedValue(REVISIONS);
    ctx.containersByApp = {
      a1: [
        { container_id: "c1", name: "shop-web-1", status_code: 1, exit_code: 0 },
        { container_id: "c2", name: "shop-worker-1", status_code: 3, exit_code: 0 },
        { container_id: "c3", name: "shop-db-1", status_code: 4, exit_code: 137 },
      ],
    };
  });

  it("shows the status with each container and what is wrong with it, and restarts", async () => {
    const user = userEvent.setup();
    const props = renderOverview();
    const status = screen.getByRole("region", { name: "Status" });
    expect(within(status).getByText("Running")).toBeInTheDocument();
    const containers = within(status).getByRole("list", { name: "Containers" });
    expect(within(containers).getAllByRole("listitem")).toHaveLength(3);
    expect(containers).toHaveTextContent("shop-worker-1Keeps restarting");
    expect(containers).toHaveTextContent("shop-db-1Stopped with exit code 137");
    await user.click(within(status).getByRole("button", { name: "Restart" }));
    expect(props.onControl).toHaveBeenCalledWith("restart");
  });

  it("offers Start for a stopped app, and says when no container reported yet", async () => {
    const user = userEvent.setup();
    ctx.containersByApp = {};
    const props = renderOverview({ status: 5 });
    const status = screen.getByRole("region", { name: "Status" });
    expect(within(status).getByText("No containers reported yet.")).toBeInTheDocument();
    await user.click(within(status).getByRole("button", { name: "Start" }));
    expect(props.onControl).toHaveBeenCalledWith("start");
  });

  it("lists the addresses with their HTTPS state", () => {
    renderOverview({
      app: {
        domains: [
          { domain: "shop.example.com", ssl: true, kind: "route" },
          { domain: "shop.lan", ssl: false, kind: "route" },
        ],
      },
    });
    const access = screen.getByRole("region", { name: "Access" });
    expect(within(access).getByRole("link", { name: /shop\.example\.com/ })).toHaveAttribute("href", "https://shop.example.com");
    expect(access).toHaveTextContent("HTTPS");
    expect(access).toHaveTextContent("HTTP only");
    expect(within(access).queryByRole("link", { name: "Add a domain" })).not.toBeInTheDocument();
  });

  it("offers Add a domain without an address, only with the built-in proxy", () => {
    renderOverview();
    const access = screen.getByRole("region", { name: "Access" });
    expect(access).toHaveTextContent("No public address.");
    expect(within(access).getByRole("link", { name: "Add a domain" })).toHaveAttribute("href", "/server/srv-1/app/a1?tab=configuration");
  });

  it("has no Add a domain on a server without the proxy", () => {
    ingress = false;
    renderOverview();
    expect(within(screen.getByRole("region", { name: "Access" })).queryByRole("link", { name: "Add a domain" })).not.toBeInTheDocument();
  });

  it("shows the resource blocks and leads to the metrics", () => {
    renderOverview();
    const resources = screen.getByRole("region", { name: "Resources" });
    expect(within(resources).getByTestId("kpi-blocks")).toBeInTheDocument();
    expect(within(resources).getByRole("link", { name: /View metrics/ })).toHaveAttribute("href", "/server/srv-1/app/a1?tab=metrics");
  });

  it("shows each set-up backup with its state, last and next copy; backs up and restores", async () => {
    const user = userEvent.setup();
    backups.runBackup.mockResolvedValue({ unchanged: false, point: { id: "p" }, warnings: [] });
    renderOverview();
    const region = screen.getByRole("region", { name: "Backups" });
    const list = await within(region).findByRole("list", { name: "Backups" });
    // media has no storage: not listed here.
    expect(within(list).getAllByRole("listitem")).toHaveLength(2);
    const db = within(list).getByText("database").closest("li")!;
    expect(db).toHaveTextContent("OK");
    expect(db).toHaveTextContent("Last copy 10 minutes ago");
    expect(db).toHaveTextContent(/next /);
    // A custom backup runs its own tool: no actions.
    expect(within(within(list).getByText("replica").closest("li")!).queryByRole("button")).not.toBeInTheDocument();
    await user.click(within(db).getByRole("button", { name: "Back up now: database" }));
    await waitFor(() => expect(backups.runBackup).toHaveBeenCalledWith("a1", "database"));
    await user.click(within(db).getByRole("button", { name: "Restore database" }));
    expect(await screen.findByRole("dialog", { name: "Copies" })).toHaveTextContent("copies of database");
    expect(within(region).getByRole("link", { name: /Manage backups/ })).toHaveAttribute("href", "/server/srv-1/app/a1?tab=backups");
  });

  it("invites to set up a backup when none is", async () => {
    ctx.getApp.mockResolvedValue({ backups: [LIST[1]] });
    renderOverview();
    const region = screen.getByRole("region", { name: "Backups" });
    expect(await within(region).findByRole("link", { name: "Set up a backup" })).toHaveAttribute(
      "href",
      "/server/srv-1/app/a1?tab=backups",
    );
  });

  it("retries a backups read that failed", async () => {
    const user = userEvent.setup();
    ctx.getApp.mockRejectedValueOnce(new Error("agent offline")).mockResolvedValue({ backups: LIST });
    renderOverview();
    const region = screen.getByRole("region", { name: "Backups" });
    expect(await within(region).findByText(/agent offline/)).toBeInTheDocument();
    await user.click(within(region).getByRole("button", { name: "Retry" }));
    expect(await within(region).findByRole("list", { name: "Backups" })).toBeInTheDocument();
    expect(ctx.getApp).toHaveBeenCalledTimes(2);
  });

  it("shows the last three changes, and opens the history", async () => {
    const user = userEvent.setup();
    const props = renderOverview();
    const region = screen.getByRole("region", { name: "Recent changes" });
    const list = await within(region).findByRole("list", { name: "Recent changes" });
    expect(within(list).getAllByRole("listitem").map((li) => li.textContent)).toEqual([
      expect.stringContaining("Changed .env"),
      expect.stringContaining("Changed compose.yml"),
      expect.stringContaining("rename to Shop"),
    ]);
    expect(within(list).getAllByRole("listitem")[0]).toHaveTextContent("Draft");
    expect(within(list).getAllByRole("listitem")[1]).toHaveTextContent("Live");
    await user.click(within(region).getByRole("button", { name: "View history" }));
    expect(props.onOpenHistory).toHaveBeenCalled();
  });

  it("reads the history only after the backups landed", async () => {
    let release: (v: unknown) => void = () => {};
    ctx.getApp.mockReturnValue(new Promise((resolve) => (release = resolve)));
    renderOverview();
    await new Promise((r) => setTimeout(r, 30));
    expect(ctx.getRevisions).not.toHaveBeenCalled();
    release({ backups: LIST });
    await waitFor(() => expect(ctx.getRevisions).toHaveBeenCalledTimes(1));
  });

  it("does not walk the history again when the page is opened again for the same version", async () => {
    const first = renderOverview();
    await screen.findByRole("list", { name: "Recent changes" });
    first.unmount();
    renderOverview();
    await screen.findByRole("list", { name: "Recent changes" });
    expect(ctx.getRevisions).toHaveBeenCalledTimes(1);
  });

  it("says so when the history can't be read, and when there is none", async () => {
    const user = userEvent.setup();
    ctx.getRevisions.mockRejectedValueOnce(new Error("boom")).mockResolvedValue({ current: "", deployed: "", revisions: [] });
    renderOverview();
    const region = screen.getByRole("region", { name: "Recent changes" });
    expect(await within(region).findByText(/boom/)).toBeInTheDocument();
    await user.click(within(region).getByRole("button", { name: "Retry" }));
    expect(await within(region).findByText("No changes yet.")).toBeInTheDocument();
  });

  it("reads the history again after a deploy forgot it, on this page and on the next visit", async () => {
    const first = renderOverview();
    const draft = await screen.findByRole("list", { name: "Recent changes" });
    expect(draft).toHaveTextContent("Draft");
    // Start brings the draft live without a commit: the version stays.
    ctx.getRevisions.mockResolvedValue({ ...REVISIONS, deployed: "dddd" });
    act(() => forgetRevisions("a1"));
    await waitFor(() => expect(screen.getByRole("list", { name: "Recent changes" })).toHaveTextContent("Live"));
    expect(screen.getByRole("list", { name: "Recent changes" })).not.toHaveTextContent("Draft");
    expect(ctx.getRevisions).toHaveBeenCalledTimes(2);
    first.unmount();
    // Forgotten again while away: the next visit asks the agent.
    act(() => forgetRevisions("a1"));
    renderOverview();
    await screen.findByRole("list", { name: "Recent changes" });
    expect(ctx.getRevisions).toHaveBeenCalledTimes(3);
  });
});
