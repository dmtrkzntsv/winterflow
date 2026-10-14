import type { ComponentProps } from "react";
import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { AppOverview } from "@/components/app-page/app-overview";
import { AppsContext, type App, type AppsContextValue } from "@/context/apps-context-base";
import { useAppDetail } from "@/hooks/use-app-detail";
import type { AppAddress } from "@/lib/app-summary";
import { forgetRevisions, resetRevisionsCache } from "@/lib/revisions";
import { resetHeldHealth } from "@/lib/backup-activity";
import type { BackupHealth, EffectiveBackup } from "@/lib/backups";
import type { RegisteredDomain } from "@/lib/domains";
import { flushPending } from "@/test/flush";

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
    { hash: "dddd", subject: "save Shop", timestamp: now - 60 },
    { hash: "cccc", subject: "update backups", timestamp: now - 7200 },
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

// Harness reads the app the way the page does and hands the read over.
function Harness(props: Omit<ComponentProps<typeof AppOverview>, "read">) {
  const read = useAppDetail(props.app.id, props.app.version);
  return <AppOverview {...props} read={read} />;
}

function renderOverview(
  over: { app?: Partial<App>; status?: number; registrations?: RegisteredDomain[]; ports?: AppAddress[] } = {},
) {
  const props = { onOpenHistory: vi.fn(), registrations: over.registrations, ports: over.ports };
  const view = render(
    <MemoryRouter>
      <AppsContext.Provider value={ctx as unknown as AppsContextValue}>
        <Harness app={{ ...APP, ...over.app }} status={over.status ?? 1} {...props} />
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
    toast.success.mockReset();
    ctx.getApp.mockReset().mockResolvedValue({ backups: LIST });
    ctx.getRevisions.mockReset().mockResolvedValue(REVISIONS);
    ctx.containersByApp = {
      a1: [
        { container_id: "c1", name: "wf-a1-web-1", status_code: 1, exit_code: 0 },
        { container_id: "c2", name: "wf-a1-worker-1", status_code: 3, exit_code: 0 },
        { container_id: "c3", name: "wf-a1-db-1", status_code: 4, exit_code: 137 },
      ],
    };
  });

  it("summarizes the containers by compose service, and what is wrong with them", () => {
    renderOverview();
    const status = screen.getByRole("region", { name: "Status" });
    expect(status).toHaveTextContent("1 of 3 containers running · 1 restarting · 1 with a problem");
    const containers = within(status).getByRole("list", { name: "Containers" });
    expect(within(containers).getAllByRole("listitem")).toHaveLength(3);
    expect(containers).toHaveTextContent("webworkerKeeps restartingdbStopped with exit code 137");
    expect(containers).not.toHaveTextContent("wf-a1");
    // The header owns the state and Restart.
    expect(within(status).queryByRole("button")).not.toBeInTheDocument();
    expect(within(status).queryByText("Running")).not.toBeInTheDocument();
  });

  it("keeps the replica number only where a service has several replicas", () => {
    ctx.containersByApp = {
      a1: [
        { container_id: "c1", name: "wf-a1-app-1", status_code: 1, exit_code: 0 },
        { container_id: "c2", name: "wf-a1-app-2", status_code: 1, exit_code: 0 },
        { container_id: "c3", name: "wf-a1-db-1", status_code: 1, exit_code: 0 },
      ],
    };
    renderOverview();
    const status = screen.getByRole("region", { name: "Status" });
    expect(status).toHaveTextContent("3 containers running");
    expect(within(status).getByRole("list", { name: "Containers" })).toHaveTextContent("app-1app-2db");
  });

  it("says a single container runs", () => {
    ctx.containersByApp = { a1: [{ container_id: "c1", name: "wf-a1-app-1", status_code: 1, exit_code: 0 }] };
    renderOverview();
    expect(screen.getByRole("region", { name: "Status" })).toHaveTextContent("1 container running");
  });

  it("says a stopped app runs nothing, and a running one that reported nothing has not reported", () => {
    ctx.containersByApp = {};
    const { unmount } = renderOverview({ status: 5 });
    expect(screen.getByRole("region", { name: "Status" })).toHaveTextContent("Stopped — no containers running");
    unmount();
    renderOverview({ status: 1 });
    expect(screen.getByRole("region", { name: "Status" })).toHaveTextContent("No containers reported yet");
  });

  it("lays out two independent columns", () => {
    renderOverview();
    const left = screen.getByRole("region", { name: "Status" }).parentElement!;
    const right = screen.getByRole("region", { name: "Access" }).parentElement!;
    expect(left.parentElement).toHaveClass("items-start", "lg:grid-cols-2");
    expect(within(left).getByRole("region", { name: "Resources" })).toBeInTheDocument();
    expect(within(left).getByRole("region", { name: "Recent changes" })).toBeInTheDocument();
    expect(within(right).getByRole("region", { name: "Backups" })).toBeInTheDocument();
  });

  it("lists the addresses with how their registered domain serves them", () => {
    renderOverview({
      app: {
        domains: [
          { domain: "shop.example.com", ssl: false, kind: "route" },
          { domain: "cloud.home.example", ssl: false, kind: "route" },
          { domain: "files.example.org", ssl: true, kind: "route" },
          { domain: "shop.lan", ssl: true, kind: "route" },
        ],
      },
      registrations: [
        { name: "shop.example.com", tls: "auto" },
        { name: "*.home.example", tls: "off" },
        { name: "files.example.org", tls: "certificate" },
      ],
    });
    const access = screen.getByRole("region", { name: "Access" });
    const row = (host: string) => within(access).getByRole("link", { name: new RegExp(host.replace(/\./g, "\\.")) }).closest("li")!;
    // The route's own ssl flag is ignored: the domain's TLS mode decides.
    expect(within(row("shop.example.com")).getByRole("link")).toHaveAttribute("href", "https://shop.example.com");
    expect(row("shop.example.com")).toHaveTextContent("HTTPS");
    expect(within(row("files.example.org")).getByRole("link")).toHaveAttribute("href", "https://files.example.org");
    expect(row("files.example.org")).toHaveTextContent("HTTPS");
    // TLS off: the tunnel or proxy in front serves HTTPS.
    expect(within(row("cloud.home.example")).getByRole("link")).toHaveAttribute("href", "https://cloud.home.example");
    expect(row("cloud.home.example")).toHaveTextContent("Behind your proxy");
    // No registration covers it: no claim either way.
    expect(row("shop.lan")).not.toHaveTextContent(/HTTPS|proxy|HTTP only/);
    expect(access).not.toHaveTextContent("HTTP only");
    expect(within(access).queryByRole("link", { name: "Add a domain" })).not.toBeInTheDocument();
  });

  it("lists the published ports after the domains, with their service, and copies them", async () => {
    const user = userEvent.setup();
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true });
    renderOverview({
      app: { domains: [{ domain: "shop.example.com", ssl: true, kind: "route" }] },
      ports: [{ host: "192.168.1.66:8090", url: "http://192.168.1.66:8090", tls: null, service: "web" }],
    });
    const access = screen.getByRole("region", { name: "Access" });
    const links = within(access).getAllByRole("link");
    expect(links.map((l) => l.getAttribute("href"))).toEqual(["https://shop.example.com", "http://192.168.1.66:8090"]);
    expect(links[1].closest("li")).toHaveTextContent("web");
    await user.click(within(access).getByRole("button", { name: "Copy http://192.168.1.66:8090" }));
    expect(writeText).toHaveBeenCalledWith("http://192.168.1.66:8090");
    expect(toast.success).toHaveBeenCalledWith("Copied");
  });

  it("shows a port range and a udp port as one row each, not as links", async () => {
    const user = userEvent.setup();
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true });
    renderOverview({
      ports: [
        { host: "192.168.1.66:8000–8010", url: "", tls: null, service: "media" },
        { host: "192.168.1.66:51820/udp", url: "", tls: null, service: "vpn" },
      ],
    });
    const access = screen.getByRole("region", { name: "Access" });
    expect(within(access).queryAllByRole("link")).toEqual([]);
    expect(within(access).getAllByRole("listitem")).toHaveLength(2);
    expect(within(access).getByText("192.168.1.66:8000–8010").closest("li")).toHaveTextContent("media");
    await user.click(within(access).getByRole("button", { name: "Copy 192.168.1.66:51820/udp" }));
    expect(writeText).toHaveBeenCalledWith("192.168.1.66:51820/udp");
  });

  it("says so when an address can't be copied", async () => {
    const user = userEvent.setup();
    Object.defineProperty(navigator, "clipboard", { value: undefined, configurable: true });
    const exec = vi.fn().mockReturnValue(false);
    Object.defineProperty(document, "execCommand", { value: exec, configurable: true });
    renderOverview({ ports: [{ host: "10.0.0.2:80", url: "http://10.0.0.2:80", tls: null }] });
    await user.click(screen.getByRole("button", { name: "Copy http://10.0.0.2:80" }));
    expect(toast.error).toHaveBeenCalledWith(expect.stringContaining("Couldn't copy"));
  });

  it("puts Access and Backups right after Status below lg, Resources last", () => {
    renderOverview();
    const order = (name: string) => [...screen.getByRole("region", { name }).classList].find((c) => /^order-\d$/.test(c));
    expect(["Status", "Access", "Backups", "Recent changes", "Resources"].map(order)).toEqual([
      "order-1",
      "order-2",
      "order-3",
      "order-4",
      "order-5",
    ]);
    expect(screen.getByRole("region", { name: "Status" }).parentElement).toHaveClass("max-lg:contents");
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

  it("says the backups can't be read, not that none is set up, and retries", async () => {
    const user = userEvent.setup();
    ctx.getApp
      .mockResolvedValueOnce({ backups: [], backups_error: 'x-winterflow.backups[0].volume: "gone" is not a volume' })
      .mockResolvedValue({ backups: LIST });
    renderOverview();
    const region = screen.getByRole("region", { name: "Backups" });
    expect(await within(region).findByText(/Backups can't be read right now: x-winterflow/)).toBeInTheDocument();
    expect(within(region).queryByText("No backup is set up yet.")).not.toBeInTheDocument();
    expect(within(region).queryByRole("link", { name: "Set up a backup" })).not.toBeInTheDocument();
    // The Backups tab is a click away.
    expect(within(region).getByRole("link", { name: "Open Backups" })).toHaveAttribute("href", expect.stringContaining("tab=backups"));
    await user.click(within(region).getByRole("button", { name: "Retry" }));
    expect(await within(region).findByRole("list", { name: "Backups" })).toBeInTheDocument();
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
      expect.stringContaining("Saved"),
      expect.stringContaining("Changed backups"),
      expect.stringContaining("Renamed to Shop"),
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
    await waitFor(() => expect(ctx.getApp).toHaveBeenCalled());
    await flushPending();
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
