import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Link, MemoryRouter, Outlet, Route, Routes, useLocation } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { AppsContext, type App, type AppsContextValue } from "@/context/apps-context-base";
import { CommandPaletteProvider } from "@/context/palette-context";
import { NotificationsContext, type NotificationsContextValue } from "@/context/notifications-context-base";
import { useCommandPalette } from "@/context/use-palette";
import AppDetailsPage from "@/pages/app-details";
import type { BackupHealth } from "@/lib/backups";
import type { InstanceView, TemplateView } from "@/lib/templates";
import type { DraftState } from "@/lib/template-draft";
import { trackActivity } from "@/lib/backup-activity";
import { fakeAPI } from "@/test/fake-api";

const toast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn(), warning: vi.fn(), info: vi.fn() }));
vi.mock("sonner", () => ({ toast }));
vi.mock("@/components/app-backups", () => ({ AppBackups: () => <p>the backups tab</p> }));
vi.mock("@/components/app-metrics", () => ({ AppMetricsTab: () => null }));
// The Overview has its own tests; here it shows whose it is and offers
// View history.
vi.mock("@/components/app-page/app-overview", () => ({
  AppOverview: ({ app, onOpenHistory }: { app: { name: string }; onOpenHistory: () => void }) => (
    <div>
      <p>the overview of {app.name}</p>
      <button type="button" onClick={onOpenHistory}>
        View history
      </button>
    </div>
  ),
}));
// The firing banner has its own tests (it polls); here it only has to be placed.
vi.mock("@/components/alerts/firing-banner", () => ({
  FiringBanner: (p: { serverId: string; appId?: string }) => (
    <div data-testid="firing-banner">{`${p.serverId}/${p.appId ?? ""}`}</div>
  ),
}));
const backupsApi = vi.hoisted(() => ({ runBackup: vi.fn() }));
vi.mock("@/context/use-backups", () => ({ useBackups: () => backupsApi }));
// The stale shared-variables badge has its own tests (it subscribes to
// notifications).
vi.mock("@/components/stale-vars-badge", () => ({ StaleVarsBadge: () => null }));
vi.mock("@/components/app-editor-panel", () => ({
  AppEditorPanel: ({ onOpenHistory, reloadSignal }: { onOpenHistory?: () => void; reloadSignal?: number }) => (
    <div>
      <p>{`reload signal ${reloadSignal}`}</p>
      <button type="button" onClick={onOpenHistory}>
        History
      </button>
      <p>the plain editor</p>
    </div>
  ),
}));
vi.mock("@/components/code-editor", () => ({
  CodeEditor: ({ value, filename }: { value: string; filename?: string }) => (
    <pre aria-label={`Content of ${filename}`}>{value}</pre>
  ),
}));
vi.mock("@/lib/ecies", () => ({
  ENCRYPTED: "<encrypted>",
  encryptSecret: vi.fn(async (value: string, key: string) => `ct(${key}):${value}`),
}));
// Links point into the active server's scope (/server/:id/...).
vi.mock("@/context/use-servers", () => ({ useServers: () => ({ activeServerId: "srv-1" }) }));

const remove = vi.fn();
const refresh = vi.fn(async () => {});
const notifications = { connected: true, subscribe: () => () => {}, waitFor: vi.fn() } as NotificationsContextValue;

// PaletteActions lists what the page offers in ⌘K, as buttons that run it.
function PaletteActions() {
  const { groups } = useCommandPalette();
  return (
    <ul aria-label="Palette actions">
      {groups.flatMap((g) =>
        g.actions.map((a) => (
          <li key={`${g.heading}-${a.id}`}>
            <button type="button" disabled={a.disabled} onClick={a.run}>{`${g.heading}: ${a.label}`}</button>
          </li>
        )),
      )}
    </ul>
  );
}

// NewTemplate stands in for the template editor Save as template opens.
function NewTemplate() {
  const state = useLocation().state as DraftState;
  return (
    <p>
      new template {state.draft.name} with {state.draft.parameters.length} parameters from {state.appName}
    </p>
  );
}

function renderPage(
  url: string,
  backupsByApp: Record<string, BackupHealth[]> = {},
  over: { app?: Partial<App>; ctx?: Partial<AppsContextValue> } = {},
) {
  const value = {
    apps: [{ id: "a1", serverId: "s", name: "Shop", version: "3", icon: "", color: "", createdAt: "", ...over.app }],
    statusByApp: { a1: 1 },
    backupsByApp,
    control: vi.fn(),
    getRevisions: vi.fn(async () => ({ current: "", deployed: "", revisions: [] })),
    getApp: vi.fn(async () => ({
      app: { app_id: "a1", config: btoa(JSON.stringify({ name: "Shop", description: "" })), files: [], variables: [] },
      revision: 1,
      available_revisions: null,
    })),
    remove,
    rename: vi.fn(),
    refresh,
    ...over.ctx,
  } as unknown as AppsContextValue;
  return render(
    <MemoryRouter initialEntries={[url]}>
      <NotificationsContext.Provider value={notifications}>
        <AppsContext.Provider value={value}>
          <CommandPaletteProvider>
          <Routes>
            <Route
              element={
                <>
                  <Link to="/app/a2?tab=editor">go to a2</Link>
                  <Link to="/app/a2?tab=settings">open a2 settings</Link>
                  <PaletteActions />
                  <Outlet />
                </>
              }
            >
              <Route path="/app/:appId" element={<AppDetailsPage />} />
              <Route path="/server/srv-1" element={<p>the apps</p>} />
              <Route path="/templates/new" element={<NewTemplate />} />
            </Route>
          </Routes>
          </CommandPaletteProvider>
        </AppsContext.Provider>
      </NotificationsContext.Provider>
    </MemoryRouter>,
  );
}

describe("AppDetailsPage", () => {
  beforeEach(() => {
    remove.mockReset();
    refresh.mockClear();
    for (const f of Object.values(toast)) f.mockReset();
  });

  it("offers its actions in the ⌘K palette", async () => {
    const user = userEvent.setup();
    const control = vi.fn(async () => {});
    renderPage("/app/a1?tab=backups", {}, { ctx: { control } });
    const list = await screen.findByRole("list", { name: "Palette actions" });
    expect(within(list).getAllByRole("button").map((b) => b.textContent)).toEqual([
      "Shop: Restart",
      "Shop: Stop",
      "Shop: Pull latest images",
      "Shop: View logs",
      "Shop: Back up now",
    ]);
    await user.click(within(list).getByRole("button", { name: "Shop: Stop" }));
    expect(control).toHaveBeenCalledWith("a1", "stop");
  });

  it("offers Start instead of Stop while the app is stopped", async () => {
    renderPage("/app/a1?tab=backups", {}, { ctx: { statusByApp: { a1: 5 } } });
    const list = await screen.findByRole("list", { name: "Palette actions" });
    expect(within(list).getByRole("button", { name: "Shop: Start" })).toBeInTheDocument();
    expect(within(list).queryByRole("button", { name: "Shop: Stop" })).not.toBeInTheDocument();
  });

  it("offers nothing in the palette for an app that isn't there", async () => {
    renderPage("/app/zzz?tab=backups");
    const list = await screen.findByRole("list", { name: "Palette actions" });
    expect(within(list).queryAllByRole("button")).toHaveLength(0);
  });

  it("disables the palette's control actions while one is running", async () => {
    const user = userEvent.setup();
    const control = vi.fn(() => new Promise<void>(() => {}));
    renderPage("/app/a1?tab=backups", {}, { ctx: { control } });
    const list = await screen.findByRole("list", { name: "Palette actions" });
    await user.click(within(list).getByRole("button", { name: "Shop: Restart" }));
    expect(control).toHaveBeenCalledTimes(1);
    await waitFor(() => expect(within(list).getByRole("button", { name: "Shop: Restart" })).toBeDisabled());
    expect(within(list).getByRole("button", { name: "Shop: Stop" })).toBeDisabled();
    expect(within(list).getByRole("button", { name: "Shop: Pull latest images" })).toBeDisabled();
    expect(within(list).getByRole("button", { name: "Shop: View logs" })).toBeEnabled();
  });

  it("opens Overview by default; old and e-mailed links still open their tab", async () => {
    const { unmount } = renderPage("/app/a1");
    expect(await screen.findByRole("tab", { name: "Overview" })).toHaveAttribute("aria-selected", "true");
    expect(screen.getByText("the overview of Shop")).toBeInTheDocument();
    expect(screen.getAllByRole("tab").map((t) => t.textContent)).toEqual([
      "Overview",
      "Logs",
      "Configuration",
      "Backups",
      "Metrics",
      "Settings",
    ]);
    unmount();
    renderPage("/app/a1?tab=metrics");
    expect(await screen.findByRole("tab", { name: "Metrics" })).toHaveAttribute("aria-selected", "true");
  });

  it("places the firing banner for this app under the header", async () => {
    renderPage("/app/a1?tab=backups");
    expect(await screen.findByTestId("firing-banner")).toHaveTextContent("s/a1");
  });

  it("shows where the app lives, and the header stacks on a phone", async () => {
    renderPage("/app/a1?tab=backups", {}, { app: { domains: [{ domain: "shop.example.com", ssl: true, kind: "route" }] } });
    expect(await screen.findByRole("link", { name: /Open/ })).toHaveAttribute("href", "https://shop.example.com");
    expect(screen.getByRole("heading", { level: 1, name: "Shop" }).closest("header")).toHaveClass("flex-col", "sm:flex-row");
  });

  it("puts Restart first while the app runs and the rest in ⋯", async () => {
    const user = userEvent.setup();
    const control = vi.fn(async () => {});
    renderPage("/app/a1?tab=backups", {}, { ctx: { control } });
    await user.click(await screen.findByRole("button", { name: "Restart" }));
    expect(control).toHaveBeenCalledWith("a1", "restart");
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith("App restarted", { description: "Shop" }));
    await user.click(screen.getByRole("button", { name: "More actions" }));
    expect((await screen.findAllByRole("menuitem")).map((m) => m.textContent)).toEqual([
      "Stop",
      "Pull latest images",
      "Back up now",
      "Save as template",
      "Delete…",
    ]);
    await user.click(screen.getByRole("menuitem", { name: "Pull latest images" }));
    expect(control).toHaveBeenCalledWith("a1", "update");
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith("Latest images pulled", { description: "Shop" }));
  });

  it("offers Start while the app is stopped, and no Stop", async () => {
    const user = userEvent.setup();
    renderPage("/app/a1?tab=backups", {}, { ctx: { statusByApp: { a1: 5 } } });
    expect(await screen.findByRole("button", { name: "Start" })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "More actions" }));
    expect(screen.queryByRole("menuitem", { name: "Stop" })).not.toBeInTheDocument();
  });

  it("backs up now from ⋯, and from ⌘K says when there is nothing to back up", async () => {
    const user = userEvent.setup();
    backupsApi.runBackup.mockReset().mockResolvedValue({ unchanged: false, point: { id: "p" }, warnings: [] });
    const getApp = vi.fn(async () => ({
      backups: [{ name: "db", type: "sqlite", enabled: true, storage: "disk", in_recipe: true, from_recipe: null }],
    }));
    renderPage("/app/a1?tab=backups", {}, { ctx: { getApp } as unknown as Partial<AppsContextValue> });
    await user.click(await screen.findByRole("button", { name: "More actions" }));
    await user.click(await screen.findByRole("menuitem", { name: "Back up now" }));
    await waitFor(() => expect(backupsApi.runBackup).toHaveBeenCalledWith("a1", "db"));

    getApp.mockResolvedValueOnce({ backups: [] } as never);
    await user.click(screen.getByRole("button", { name: "Shop: Back up now" }));
    await waitFor(() =>
      expect(toast.info).toHaveBeenCalledWith("Nothing to back up yet", { description: "Set up a backup on the Backups tab first." }),
    );
  });

  it("disables Back up now in ⋯ while a backup of the app runs or is restored", async () => {
    const user = userEvent.setup();
    let end = () => {};
    act(() => {
      end = trackActivity("a1", "db", "restore");
    });
    renderPage("/app/a1?tab=backups");
    await user.click(await screen.findByRole("button", { name: "More actions" }));
    expect(await screen.findByRole("menuitem", { name: "Back up now" })).toHaveAttribute("aria-disabled", "true");
    await user.keyboard("{Escape}");
    act(() => end());
    await user.click(screen.getByRole("button", { name: "More actions" }));
    expect(await screen.findByRole("menuitem", { name: "Back up now" })).not.toHaveAttribute("aria-disabled");
  });

  it("shows in the header what a running action from ⋯ is doing", async () => {
    const user = userEvent.setup();
    const control = vi.fn(() => new Promise<void>(() => {}));
    renderPage("/app/a1?tab=backups", {}, { ctx: { control } });
    await user.click(await screen.findByRole("button", { name: "More actions" }));
    await user.click(await screen.findByRole("menuitem", { name: "Stop" }));
    expect(await screen.findByRole("status")).toHaveTextContent("Stopping…");
  });

  it("shows in the header that a backup runs or a restore is under way", async () => {
    let endBackup = () => {};
    let endRestore = () => {};
    act(() => {
      endBackup = trackActivity("a1", "db", "backup");
    });
    renderPage("/app/a1?tab=backups");
    expect(await screen.findByRole("status")).toHaveTextContent("Backing up…");
    act(() => {
      endRestore = trackActivity("a1", "db", "restore");
    });
    expect(screen.getByRole("status")).toHaveTextContent("Restoring…");
    act(() => {
      endRestore();
      endBackup();
    });
    await waitFor(() => expect(screen.queryByRole("status")).not.toBeInTheDocument());
  });

  it("deletes from ⋯ through the one Delete dialog", async () => {
    const user = userEvent.setup();
    renderPage("/app/a1?tab=backups");
    await user.click(await screen.findByRole("button", { name: "More actions" }));
    await user.click(await screen.findByRole("menuitem", { name: "Delete…" }));
    expect(await screen.findByRole("alertdialog", { name: "Delete Shop?" })).toBeInTheDocument();
  });

  it("opens History from the Overview", async () => {
    const user = userEvent.setup();
    renderPage("/app/a1");
    await user.click(await screen.findByRole("button", { name: "View history" }));
    expect(await screen.findByRole("dialog", { name: "History" })).toBeInTheDocument();
  });

  it("shows only the new app's settings after moving to another app", async () => {
    const user = userEvent.setup();
    const getApp = vi.fn(async (id: string) => ({
      app: { app_id: id, config: btoa(JSON.stringify({ name: id, description: `${id} notes` })), files: [], variables: [] },
      revision: 1,
      available_revisions: null,
    }));
    const apps = [
      { id: "a1", serverId: "s", name: "Shop", version: "3", icon: "", color: "", createdAt: "" },
      { id: "a2", serverId: "s", name: "Blog", version: "1", icon: "", color: "", createdAt: "" },
    ];
    renderPage("/app/a1?tab=settings", {}, { ctx: { apps, getApp } as unknown as Partial<AppsContextValue> });
    expect(await screen.findByDisplayValue("a1 notes")).toBeInTheDocument();
    await user.click(screen.getByRole("link", { name: "open a2 settings" }));
    expect(await screen.findByDisplayValue("a2 notes")).toBeInTheDocument();
    expect(screen.queryByDisplayValue("a1 notes")).not.toBeInTheDocument();
    expect(screen.getByRole("heading", { level: 1, name: "Blog" })).toBeInTheDocument();
    expect(screen.getByRole("textbox", { name: "App name" })).toHaveValue("Blog");
  });

  it("keeps a running action with its app: the next app's controls stay free, and the toast names the app", async () => {
    const user = userEvent.setup();
    let finish = () => {};
    const control = vi.fn(() => new Promise<void>((r) => (finish = r)));
    const apps = [
      { id: "a1", serverId: "s", name: "Shop", version: "3", icon: "", color: "", createdAt: "" },
      { id: "a2", serverId: "s", name: "Blog", version: "1", icon: "", color: "", createdAt: "" },
    ];
    renderPage("/app/a1?tab=settings", {}, { ctx: { apps, control, statusByApp: { a1: 1, a2: 1 } } as unknown as Partial<AppsContextValue> });
    await user.click(await screen.findByRole("button", { name: "More actions" }));
    await user.click(await screen.findByRole("menuitem", { name: "Stop" }));
    expect(await screen.findByRole("status")).toHaveTextContent("Stopping…");

    await user.click(screen.getByRole("link", { name: "open a2 settings" }));
    expect(await screen.findByRole("heading", { level: 1, name: "Blog" })).toBeInTheDocument();
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Restart" })).toBeEnabled();
    expect(screen.getByRole("button", { name: "Blog: Stop" })).toBeEnabled();

    finish();
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith("App stopped", { description: "Shop" }));
    expect(screen.getByRole("button", { name: "Restart" })).toBeEnabled();
  });

  it("names a nameless app Unnamed app everywhere, never by its id", async () => {
    renderPage("/app/a1?tab=backups", {}, { app: { name: "" } });
    expect(await screen.findByRole("heading", { level: 1, name: "Unnamed app" })).toBeInTheDocument();
    expect(await screen.findByRole("button", { name: "Unnamed app: Restart" })).toBeInTheDocument();
  });

  it("opens Configuration from its new name and from the old ?tab=editor", async () => {
    const { unmount } = renderPage("/app/a1?tab=configuration");
    expect(await screen.findByRole("tab", { name: "Configuration" })).toHaveAttribute("aria-selected", "true");
    unmount();
    renderPage("/app/a1?tab=editor");
    expect(await screen.findByRole("tab", { name: "Configuration" })).toHaveAttribute("aria-selected", "true");
  });

  it("opens History from Configuration's bar; History is no tab any more", async () => {
    const user = userEvent.setup();
    const getRevisions = vi.fn(async () => ({
      current: "bbbbbbbb",
      deployed: "bbbbbbbb",
      revisions: [{ hash: "bbbbbbbb", subject: "Changed compose.yml", timestamp: 2 }],
    }));
    renderPage("/app/a1?tab=configuration", {}, { ctx: { getRevisions } });
    expect(screen.queryByRole("tab", { name: "History" })).not.toBeInTheDocument();
    await user.click(await screen.findByRole("button", { name: "History" }));
    const sheet = await screen.findByRole("dialog", { name: "History" });
    expect(await within(sheet).findByText("Changed compose.yml")).toBeInTheDocument();
  });

  it("tells the Configuration editor to read the app again after a roll back in History", async () => {
    const user = userEvent.setup();
    const getRevisions = vi.fn(async () => ({
      current: "bbbbbbbb",
      deployed: "bbbbbbbb",
      revisions: [
        { hash: "bbbbbbbb", subject: "edit", timestamp: 2 },
        { hash: "aaaaaaaa", subject: "create", timestamp: 1 },
      ],
    }));
    const rollback = vi.fn(async () => {});
    renderPage("/app/a1?tab=configuration", {}, { ctx: { getRevisions, rollback } });
    expect(await screen.findByText("reload signal 0")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "History" }));
    const sheet = await screen.findByRole("dialog", { name: "History" });
    await user.click(await within(sheet).findByRole("button", { name: /Roll back/ }));
    await user.click(within(await screen.findByRole("alertdialog")).getByRole("button", { name: "Roll back" }));
    await waitFor(() => expect(rollback).toHaveBeenCalledWith("a1", "aaaaaaaa"));
    expect(await screen.findByText("reload signal 1")).toBeInTheDocument();
  });

  it("marks the Backups tab when a backup is late or failing", async () => {
    renderPage("/app/a1?tab=backups", { a1: [{ name: "db", state: "failing", last_error: "disk full" }] });
    const tab = await screen.findByRole("tab", { name: /Backups/ });
    expect(tab.querySelector('[aria-label="A backup is late or failing"]')).not.toBeNull();
    expect(screen.getByText("the backups tab")).toBeInTheDocument();
  });

  it("has no mark while the backups are fine", async () => {
    renderPage("/app/a1?tab=backups", { a1: [{ name: "db", state: "ok" }] });
    const tab = await screen.findByRole("tab", { name: /Backups/ });
    expect(tab.querySelector('[aria-label="A backup is late or failing"]')).toBeNull();
  });

  it("shows progress while an app is deleted, and where its backups stayed", async () => {
    const user = userEvent.setup({ delay: null });
    let finish: (v: unknown) => void = () => {};
    remove.mockReturnValue(new Promise((resolve) => (finish = resolve)));
    renderPage("/app/a1?tab=settings", { a1: [{ name: "db", state: "ok" }] });
    await user.click(await screen.findByRole("button", { name: "Delete app…" }));
    await user.click(screen.getByRole("button", { name: "Delete app" }));
    expect(await screen.findByRole("button", { name: /Deleting Shop/ })).toBeDisabled();
    expect(screen.getByRole("status")).toHaveTextContent("This can take a few minutes");
    expect(remove).toHaveBeenCalledWith("a1", { deleteVolumes: false, deleteBackups: false });
    await act(async () => finish({ warnings: [], volumesDeleted: false, backupsDeleted: false }));
    await waitFor(() =>
      expect(toast.success).toHaveBeenCalledWith("App deleted", { description: expect.stringContaining("Other apps' backups") }),
    );
    // The deleted app's page is left for the list.
    expect(await screen.findByText("the apps")).toBeInTheDocument();
  });

  it("keeps the dialog after a failed delete, then reports a delete with warnings", async () => {
    const user = userEvent.setup({ delay: null });
    remove.mockRejectedValueOnce(new Error("timed out"));
    renderPage("/app/a1?tab=settings");
    await user.click(await screen.findByRole("button", { name: "Delete app…" }));
    await user.click(screen.getByRole("button", { name: "Delete app" }));
    await waitFor(() => expect(toast.error).toHaveBeenCalledWith("Delete failed", { description: "timed out" }));
    remove.mockResolvedValueOnce({ warnings: ["volume x is in use"], volumesDeleted: false, backupsDeleted: false });
    // The dialog is still open: confirm again.
    await user.click(await screen.findByRole("button", { name: "Delete app" }));
    await waitFor(() =>
      expect(toast.warning).toHaveBeenCalledWith("App deleted with cleanup warnings", { description: "volume x is in use" }),
    );
    expect(await screen.findByText("the apps")).toBeInTheDocument();
  });
});

const INSTANCE: InstanceView = {
  key: "k1",
  server_id: "s",
  app_id: "a1",
  template_id: "t1",
  template_name: "twillingate",
  revision: 2,
  parameters: { NAME: "bakomi" },
  secrets: { SESSION_KEY: "<encrypted>" },
  state: "created",
  status_code: 1,
  last_error: "",
  created_at: "2026-10-12T10:00:00Z",
  updated_at: "2026-10-12T10:00:00Z",
};

const template = (revision: number, extra: TemplateView["body"]["parameters"] = []): TemplateView => ({
  id: "t1",
  name: "twillingate",
  latest_revision: 3,
  revision,
  body: {
    files: { "compose.yaml": "services:\n  app:\n    image: twillingate:${VERSION}\n" },
    parameters: [{ name: "NAME", required: true }, { name: "SESSION_KEY", secret: true, required: true }, ...(extra ?? [])],
    app_name: "${NAME}",
    icon: "",
    color: "",
    ingress: null,
    backups: null,
  },
  note: "",
  created_at: "2026-10-12T10:00:00Z",
  created_by: "u1",
  revisions: null,
});

// instanceAPI answers the instance lookup: the instance, its revision and
// the latest (which adds a required REGION).
function instanceAPI(inst: InstanceView = INSTANCE, extra: Record<string, unknown> = {}) {
  return fakeAPI({
    "/api/v1/instance/get-instances": { instances: [inst] },
    "/api/v1/template/get-template": (_: unknown, q: URLSearchParams) =>
      q.get("revision") ? template(Number(q.get("revision"))) : template(3, [{ name: "REGION", required: true }]),
    "/api/v1/server/get-public-key": { public_key: "K" },
    ...extra,
  });
}

const managed = { app: { serverId: "s", templateId: "t1" } };

describe("AppDetailsPage of a template instance", () => {
  afterEach(() => vi.unstubAllGlobals());
  beforeEach(() => {
    refresh.mockClear();
    for (const f of Object.values(toast)) f.mockReset();
  });

  it("chips the template, and updates asking for the new value", async () => {
    const user = userEvent.setup();
    const api = instanceAPI(INSTANCE, { "/api/v1/instance/upgrade-instance": { instance: INSTANCE, request_id: "", warnings: [] } });
    renderPage("/app/a1?tab=backups", {}, managed);
    expect(await screen.findByRole("link", { name: "From template twillingate" })).toHaveAttribute("href", "/templates/t1");
    expect(screen.queryByText(/r3/)).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Update available" }));
    const dialog = await screen.findByRole("dialog");
    await user.type(within(dialog).getByLabelText("REGION"), "eu");
    await user.click(within(dialog).getByRole("button", { name: "Upgrade to r3" }));
    await waitFor(() => expect(refresh).toHaveBeenCalled());
    expect(api.posted("/api/v1/instance/upgrade-instance")).toEqual([
      { server_id: "s", key: "k1", revision: 3, parameters: { REGION: "eu" } },
    ]);
  });

  it("titles the upgrade with the app, reuses the revision read, and says a stopped app starts", async () => {
    const user = userEvent.setup();
    const api = instanceAPI();
    renderPage("/app/a1?tab=backups", {}, { ...managed, ctx: { statusByApp: { a1: 5 } } });
    await user.click(await screen.findByRole("button", { name: "Update available" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByRole("heading")).toHaveTextContent("Upgrade Shop to r3");
    expect(dialog).toHaveTextContent("The app will be started.");
    // The page read r2 already: the dialog doesn't read it again.
    expect(api.calls.filter((c) => c.query.includes("revision=2"))).toHaveLength(1);
  });

  it("hides the app's own revision for an app a template manages", async () => {
    instanceAPI();
    renderPage("/app/a1?tab=backups", {}, managed);
    expect(await screen.findByRole("link", { name: "From template twillingate" })).toBeInTheDocument();
    expect(screen.queryByText("Revision 3")).not.toBeInTheDocument();
  });

  it("never shows a revision number in the header", async () => {
    renderPage("/app/a1?tab=backups");
    expect(await screen.findByRole("heading", { level: 1, name: "Shop" })).toBeInTheDocument();
    expect(screen.queryByText(/Revision/)).not.toBeInTheDocument();
  });

  it("offers the newer template version from the Configuration tab", async () => {
    const user = userEvent.setup();
    instanceAPI();
    renderPage("/app/a1?tab=configuration", {}, managed);
    const note = await screen.findByText(/A newer version of twillingate is available/);
    expect(note).toHaveTextContent("(version 3)");
    await user.click(within(note.closest('[role="status"]') as HTMLElement).getByRole("button", { name: "Update" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByRole("heading")).toHaveTextContent("Upgrade Shop to r3");
  });

  it("shows the files read-only and a Parameters form instead of the editor", async () => {
    const user = userEvent.setup();
    const api = instanceAPI(INSTANCE, { "/api/v1/instance/update-instance": { instance: INSTANCE, request_id: "", warnings: [] } });
    renderPage("/app/a1?tab=editor", {}, managed);
    expect(await screen.findByText(/Managed by template/)).toBeInTheDocument();
    expect(screen.queryByText("the plain editor")).not.toBeInTheDocument();
    expect(screen.getByLabelText("Content of compose.yaml")).toHaveTextContent("image: twillingate:${VERSION}");
    const save = screen.getByRole("button", { name: "Save & redeploy" });
    expect(save).toBeDisabled();
    expect(screen.getByLabelText("SESSION_KEY")).toHaveAttribute("placeholder", "Stored, leave blank to keep");
    await user.clear(screen.getByLabelText("NAME"));
    await user.type(screen.getByLabelText("NAME"), "shop");
    await user.click(save);
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith("Saved and redeployed"));
    expect(api.posted("/api/v1/instance/update-instance")).toEqual([{ server_id: "s", key: "k1", parameters: { NAME: "shop" } }]);
    expect(refresh).toHaveBeenCalled();
  });

  it("sends a cleared value's default (the API would keep an empty value), else the empty value", async () => {
    const user = userEvent.setup();
    const extra = [{ name: "GREETING", default: "Hello" }, { name: "MOTTO" }];
    const api = instanceAPI(
      { ...INSTANCE, parameters: { NAME: "bakomi", GREETING: "Hi", MOTTO: "carpe diem" } },
      {
        "/api/v1/template/get-template": (_: unknown, q: URLSearchParams) => template(Number(q.get("revision") || 3), extra),
        "/api/v1/instance/update-instance": { instance: INSTANCE, request_id: "", warnings: [] },
      },
    );
    renderPage("/app/a1?tab=editor", {}, managed);
    await user.clear(await screen.findByLabelText("GREETING"));
    await user.clear(screen.getByLabelText("MOTTO"));
    await user.click(screen.getByRole("button", { name: "Save & redeploy" }));
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith("Saved and redeployed"));
    expect(api.posted("/api/v1/instance/update-instance")).toEqual([
      { server_id: "s", key: "k1", parameters: { GREETING: "Hello", MOTTO: "" } },
    ]);
  });

  it("replaces a secret only when a new one is typed, encrypted for the server", async () => {
    const user = userEvent.setup();
    const api = instanceAPI(INSTANCE, { "/api/v1/instance/update-instance": { instance: INSTANCE, request_id: "", warnings: [] } });
    renderPage("/app/a1?tab=editor", {}, managed);
    await user.type(await screen.findByLabelText("SESSION_KEY"), "new-secret");
    await user.click(screen.getByRole("button", { name: "Save & redeploy" }));
    await waitFor(() => expect(toast.success).toHaveBeenCalled());
    expect(api.posted("/api/v1/instance/update-instance")).toEqual([
      { server_id: "s", key: "k1", secrets: { SESSION_KEY: "ct(K):new-secret" } },
    ]);
  });

  it("shows a pending instance's failure and asks for its secrets again", async () => {
    const user = userEvent.setup();
    instanceAPI({ ...INSTANCE, state: "pending", last_error: "pull access denied" });
    renderPage("/app/a1?tab=editor", {}, managed);
    expect(await screen.findByText(/last deploy from its template failed: pull access denied/)).toBeInTheDocument();
    expect(screen.getByText("Not created yet")).toBeInTheDocument();
    expect(screen.getByLabelText("SESSION_KEY")).toHaveAttribute("placeholder", "Encrypted in your browser");
    await user.click(screen.getByRole("button", { name: "Save & redeploy" }));
    expect(screen.getByText("Required")).toBeInTheDocument();
  });

  it("shows a long deploy failure by its last line, the rest behind Details", async () => {
    instanceAPI({ ...INSTANCE, state: "pending", last_error: "Container web  Creating\nError: pull access denied\n" });
    renderPage("/app/a1?tab=editor", {}, managed);
    expect(
      await screen.findByText(
        "The app's last deploy from its template failed: Error: pull access denied. Fix its values under Configuration and save again.",
      ),
    ).toBeVisible();
    expect(screen.getByText("Details")).toBeInTheDocument();
    expect(screen.getByText(/Container web Creating/)).not.toBeVisible();
  });

  it("never offers the plain editor while the instance is being looked up", async () => {
    vi.stubGlobal("fetch", vi.fn(() => new Promise(() => {})));
    renderPage("/app/a1?tab=editor", {}, managed);
    expect(await screen.findByRole("tab", { name: "Configuration" })).toBeInTheDocument();
    expect(screen.queryByText("the plain editor")).not.toBeInTheDocument();
  });

  it("offers Retry when the lookup fails", async () => {
    const user = userEvent.setup();
    fakeAPI({ "/api/v1/instance/get-instances": new Error("database down") });
    renderPage("/app/a1?tab=editor", {}, managed);
    expect(await screen.findByText("database down")).toBeInTheDocument();
    vi.unstubAllGlobals();
    instanceAPI();
    await user.click(screen.getByRole("button", { name: "Retry" }));
    expect(await screen.findByText(/Managed by template/)).toBeInTheDocument();
  });

  it("treats an app whose instance is gone as an ordinary app", async () => {
    fakeAPI({ "/api/v1/instance/get-instances": { instances: [] } });
    renderPage("/app/a1?tab=editor", {}, managed);
    expect(await screen.findByText("the plain editor")).toBeInTheDocument();
  });

  it("does not offer Save as template for an app a template manages", async () => {
    // The instance lookup is still pending: the page has the template id but
    // no instance yet.
    vi.stubGlobal("fetch", vi.fn(() => new Promise(() => {})));
    renderPage("/app/a1?tab=settings", {}, managed);
    expect(await screen.findByRole("textbox", { name: "App name" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Save as template" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Detach from template" })).not.toBeInTheDocument();
  });

  it("detaches from the template after confirming", async () => {
    const user = userEvent.setup();
    const api = instanceAPI(INSTANCE, { "/api/v1/instance/unlink-instance": { instance: INSTANCE, request_id: "", warnings: [] } });
    renderPage("/app/a1?tab=settings", {}, managed);
    await user.click(await screen.findByRole("button", { name: "Detach from template" }));
    expect(
      screen.getByText("The app becomes an ordinary app you can edit freely; template updates no longer reach it."),
    ).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Detach" }));
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith("Detached from the template"));
    expect(api.posted("/api/v1/instance/unlink-instance")).toEqual([{ server_id: "s", key: "k1" }]);
    expect(refresh).toHaveBeenCalled();
  });

  it("offers no Detach while the instance is pending (the API refuses it)", async () => {
    instanceAPI({ ...INSTANCE, state: "pending", last_error: "pull access denied" });
    renderPage("/app/a1?tab=settings", {}, managed);
    expect(await screen.findByText(/Detach becomes available once/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Detach from template" })).not.toBeInTheDocument();
  });

  it("reports a failed detach", async () => {
    const user = userEvent.setup();
    instanceAPI(INSTANCE, { "/api/v1/instance/unlink-instance": new Error("instance not found") });
    renderPage("/app/a1?tab=settings", {}, managed);
    await user.click(await screen.findByRole("button", { name: "Detach from template" }));
    await user.click(screen.getByRole("button", { name: "Detach" }));
    await waitFor(() => expect(toast.error).toHaveBeenCalledWith("Detach failed", { description: "instance not found" }));
  });

  it("offers no Roll back in History: the template owns the files", async () => {
    instanceAPI();
    const getRevisions = vi.fn(async () => ({
      current: "bbbbbbbb",
      deployed: "bbbbbbbb",
      revisions: [
        { hash: "bbbbbbbb", subject: "upgrade", timestamp: 2 },
        { hash: "aaaaaaaa", subject: "create", timestamp: 1 },
      ],
    }));
    renderPage("/app/a1?tab=history", {}, { ...managed, ctx: { getRevisions } });
    expect(await screen.findByText(/Managed by template twillingate/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Roll back/ })).not.toBeInTheDocument();
  });

  it("re-reads the app list when a roll back is refused as managed", async () => {
    const user = userEvent.setup();
    const getRevisions = vi.fn(async () => ({
      current: "bbbbbbbb",
      deployed: "bbbbbbbb",
      revisions: [
        { hash: "bbbbbbbb", subject: "edit", timestamp: 2 },
        { hash: "aaaaaaaa", subject: "create", timestamp: 1 },
      ],
    }));
    const rollback = vi.fn(async () => {
      throw new Error("this app is managed by template twillingate: update the instance or unlink it");
    });
    renderPage("/app/a1?tab=history", {}, { ctx: { getRevisions, rollback } });
    await user.click(await screen.findByRole("button", { name: /Roll back/ }));
    await user.click(screen.getByRole("button", { name: "Roll back" }));
    await waitFor(() => expect(refresh).toHaveBeenCalled());
  });

  it("offers no Roll back while a templated app's instance is still being looked up", async () => {
    vi.stubGlobal("fetch", vi.fn(() => new Promise(() => {})));
    const getRevisions = vi.fn(async () => ({
      current: "bbbbbbbb",
      deployed: "bbbbbbbb",
      revisions: [
        { hash: "bbbbbbbb", subject: "edit", timestamp: 2 },
        { hash: "aaaaaaaa", subject: "create", timestamp: 1 },
      ],
    }));
    renderPage("/app/a1?tab=history", {}, { ...managed, ctx: { getRevisions } });
    expect(await screen.findByText(/Managed by a template/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Roll back/ })).not.toBeInTheDocument();
  });

  it("reports a template that can't be loaded for an upgrade", async () => {
    const user = userEvent.setup();
    instanceAPI(INSTANCE, {
      "/api/v1/template/get-template": (_: unknown, q: URLSearchParams) => {
        if (q.get("revision")) return template(2);
        return new Error("boom");
      },
    });
    renderPage("/app/a1?tab=backups", {}, managed);
    await user.click(await screen.findByRole("button", { name: "Update available" }));
    await waitFor(() => expect(toast.error).toHaveBeenCalledWith("Couldn't load the template", { description: "boom" }));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("shows a failed save of the parameters", async () => {
    const user = userEvent.setup();
    instanceAPI(INSTANCE, { "/api/v1/instance/update-instance": new Error("render failed") });
    renderPage("/app/a1?tab=editor", {}, managed);
    await user.clear(await screen.findByLabelText("NAME"));
    await user.type(screen.getByLabelText("NAME"), "shop");
    await user.click(screen.getByRole("button", { name: "Save & redeploy" }));
    expect(await screen.findByText("render failed")).toBeInTheDocument();
  });

  it("names the lookup failure when the error has no message", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => Promise.reject("offline")));
    renderPage("/app/a1?tab=editor", {}, managed);
    expect(await screen.findByText("Failed to load the app's template")).toBeInTheDocument();
  });

  it("never shows the first app's instance on the app the user moved to", async () => {
    const user = userEvent.setup();
    let answer: () => void = () => {};
    const reply = (data: unknown) => new Response(JSON.stringify({ success: true, message: "ok", data }));
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string) => {
        const path = new URL(url, "http://x").pathname;
        if (path === "/api/v1/instance/get-instances") {
          await new Promise<void>((resolve) => (answer = resolve));
          return reply({ instances: [INSTANCE] });
        }
        if (path === "/api/v1/template/get-template") return reply(template(2));
        return new Response("{}", { status: 404 });
      }),
    );
    const other = { id: "a2", serverId: "s", name: "Blog", version: "1", icon: "", color: "", createdAt: "" };
    renderPage("/app/a1?tab=editor", {}, {
      ctx: {
        apps: [
          { id: "a1", serverId: "s", name: "Shop", version: "3", icon: "", color: "", createdAt: "", templateId: "t1" },
          other,
        ],
      },
    });
    await user.click(await screen.findByRole("link", { name: "go to a2" }));
    expect(await screen.findByText("the plain editor")).toBeInTheDocument();
    await act(async () => answer());
    // a1's late answer must not reach a2's page.
    await new Promise((r) => setTimeout(r, 50));
    expect(screen.getByText("the plain editor")).toBeInTheDocument();
    expect(screen.queryByText(/Managed by template/)).not.toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "From template twillingate" })).not.toBeInTheDocument();
    expect(screen.queryByLabelText("NAME")).not.toBeInTheDocument();
  });
});

describe("AppDetailsPage: Save as template", () => {
  beforeEach(() => {
    for (const f of Object.values(toast)) f.mockReset();
  });

  it("opens a new template's editor built from the app", async () => {
    const user = userEvent.setup();
    const getApp = vi.fn(async () => ({
      app: {
        app_id: "a1",
        config: btoa(JSON.stringify({ name: "Shop", files: [{ filename: "compose.yml" }], variables: [{ name: "PORT" }] })),
        files: [{ name: "compose.yml", content: btoa("services: {}\n") }],
        variables: [{ name: "PORT", content: btoa("8080") }],
      },
      revision: 1,
      available_revisions: null,
    }));
    renderPage("/app/a1?tab=settings", {}, { ctx: { getApp } });
    await user.click(await screen.findByRole("button", { name: "Save as template" }));
    expect(await screen.findByText("new template shop with 1 parameters from Shop")).toBeInTheDocument();
  });

  it("reports an app it can't read", async () => {
    const user = userEvent.setup();
    const getApp = vi.fn(async () => Promise.reject(new Error("agent offline")));
    renderPage("/app/a1?tab=settings", {}, { ctx: { getApp } });
    await user.click(await screen.findByRole("button", { name: "Save as template" }));
    await waitFor(() => expect(toast.error).toHaveBeenCalledWith("Couldn't read the app", { description: "agent offline" }));
    expect(screen.getByRole("button", { name: "Save as template" })).toBeEnabled();
  });
});
