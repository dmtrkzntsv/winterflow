import { act, cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { AppBackups } from "@/components/app-backups";
import { AppsContext, type AppDetailPayload, type AppsContextValue } from "@/context/apps-context-base";
import { holdHealth, OVERLAY_HOLD_MS, resetHeldHealth, trackActivity } from "@/lib/backup-activity";
import { AgentError, type BackupHealth, type EffectiveBackup } from "@/lib/backups";

// Links point into the active server's scope (/server/:id/...).
vi.mock("@/context/use-servers", () => ({ useServers: () => ({ activeServerId: "srv-1" }) }));

const toast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn(), warning: vi.fn() }));
vi.mock("sonner", () => ({ toast }));
const backups = vi.hoisted(() => ({
  updateBackups: vi.fn(),
  runBackup: vi.fn(),
  serverStorages: vi.fn(),
  listRestorePoints: vi.fn(),
  restoreBackup: vi.fn(),
  deleteRestorePoint: vi.fn(),
}));
vi.mock("@/context/use-backups", () => ({ useBackups: () => backups }));

const all = { every: true, keep: true, volume: true, service: true, path: true, paths: true, exclude: true };
const LIST: EffectiveBackup[] = [
  {
    name: "database",
    type: "sqlite",
    enabled: true,
    storage: "disk",
    every: "1h",
    keep: { recent: "24h0m0s", daily: 14, monthly: 6 },
    volume: "data",
    path: "app.db",
    in_recipe: true,
    from_recipe: all,
  },
  {
    name: "media",
    type: "files",
    enabled: false,
    storage: "",
    every: "manual",
    keep: { recent: "24h0m0s", daily: 14, monthly: 6 },
    service: "web",
    paths: ["/app/media"],
    in_recipe: true,
    from_recipe: all,
  },
  { name: "replica", type: "custom", enabled: true, storage: "s3", service: "litestream", in_recipe: true, from_recipe: all },
];
const HEALTH: BackupHealth[] = [
  { name: "database", state: "failing", last_success: Math.floor(Date.now() / 1000) - 240, last_error: "disk full" },
  { name: "replica", state: "ok" },
];

function renderBackups(detail: Partial<AppDetailPayload> = {}, getApp?: AppsContextValue["getApp"], health: BackupHealth[] = HEALTH) {
  const value = {
    getApp: getApp ?? vi.fn().mockResolvedValue({ backups: LIST, backup_targets: { volumes: ["data"], services: [] }, retention_days: 180, backups_base: "b0", ...detail }),
    backupsByApp: { a1: health },
  } as unknown as AppsContextValue;
  return render(
    <MemoryRouter>
      <AppsContext.Provider value={value}>
        <AppBackups appId="a1" />
      </AppsContext.Provider>
    </MemoryRouter>,
  );
}

// flip turns a backup on or off the way the page does: in its Edit dialog.
async function flip(user: ReturnType<typeof userEvent.setup>, name: string) {
  await choose(user, name, "Edit…");
  const dialog = await screen.findByRole("dialog");
  await user.click(within(dialog).getByRole("switch", { name: "Back up on schedule" }));
  await user.click(within(dialog).getByRole("button", { name: "Save" }));
}

const row = (name: string) => screen.getByText(name, { selector: "span.font-mono" }).closest("li")!;

// menuItem opens a backup's ⋯ menu and returns its item named `item`.
async function menuItem(user: ReturnType<typeof userEvent.setup>, name: string, item: string) {
  await user.click(await screen.findByRole("button", { name: `Actions for ${name}` }));
  return screen.findByRole("menuitem", { name: item });
}

// choose runs an item of a backup's ⋯ menu.
async function choose(user: ReturnType<typeof userEvent.setup>, name: string, item: string) {
  await user.click(await menuItem(user, name, item));
}

describe("AppBackups", () => {
  beforeEach(() => {
    resetHeldHealth();
    for (const f of Object.values(backups)) f.mockReset();
    for (const f of Object.values(toast)) f.mockReset();
    let version = 0;
    backups.serverStorages.mockResolvedValue(["disk", "offsite"]);
    backups.updateBackups.mockImplementation(async (_app: string, list: { name: string; enabled?: boolean; storage?: string }[]) => ({
      base: `b${++version}`,
      backups: LIST.map((b) => {
        const e = list.find((c) => c.name === b.name);
        return { ...b, enabled: e?.enabled ?? false, storage: e?.storage ?? "" };
      }),
      redeployed: false,
      warnings: [],
    }));
  });

  it("says an app without volumes or bind mounts has nothing to back up, and offers no Add backup", async () => {
    renderBackups({ backups: [], backup_targets: { volumes: [], services: [{ name: "web", mounts: [] }] } });
    expect(await screen.findByText("This app keeps no data on disk, so there's nothing to back up.")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Add backup/ })).not.toBeInTheDocument();
    expect(screen.queryByText(/x-winterflow/)).not.toBeInTheDocument();
  });

  it("lists each backup with its storage, schedule, location and health", async () => {
    renderBackups();
    const db = within(await screen.findByText("database", { selector: "span.font-mono" }).then((e) => e.closest("li")!));
    expect(db.getByText("Failing")).toBeInTheDocument();
    expect(db.getByText("Last copy 4 minutes ago")).toBeInTheDocument();
    expect(db.getByText("disk full")).toBeInTheDocument();
    expect(db.getByText("SQLite database · volume data: app.db → disk · Every hour")).toBeInTheDocument();
    expect(db.queryByRole("switch")).not.toBeInTheDocument();
    expect(db.queryByRole("button", { name: /Set up/ })).not.toBeInTheDocument();
    expect(db.queryByText("sqlite")).not.toBeInTheDocument();
    expect(db.queryByText("added here")).not.toBeInTheDocument();
    const media = within(row("media"));
    expect(media.getByText("Off")).toBeInTheDocument();
    expect(media.getByText("Folders · service web: /app/media · Manual")).toBeInTheDocument();
    const replica = within(row("replica"));
    expect(replica.getByText("Custom · service litestream → s3 · Its own tool")).toBeInTheDocument();
    expect(replica.getByText("OK")).toBeInTheDocument();
    // Flat rows, no table.
    expect(screen.queryByRole("table")).not.toBeInTheDocument();
    expect(screen.getByRole("list", { name: "Backups" })).toBeInTheDocument();
  });

  it("offers Set up in the actions to a backup without a storage, and no switch anywhere", async () => {
    const user = userEvent.setup({ delay: null });
    renderBackups();
    const media = within(await screen.findByText("media", { selector: "span.font-mono" }).then((e) => e.closest("li")!));
    expect(screen.queryByRole("switch")).not.toBeInTheDocument();
    expect(media.getByRole("button", { name: "Set up media" })).toHaveTextContent("Set up…");
    expect(within(row("database")).queryByRole("button", { name: /Set up/ })).not.toBeInTheDocument();
    await user.click(media.getByRole("button", { name: "Set up media" }));
    expect(await screen.findByRole("heading", { name: "Turn on media" })).toBeInTheDocument();
  });

  it("shows the state with when the last copy was made, and when the next is due", async () => {
    const last = Math.floor(Date.now() / 1000) - 600;
    renderBackups({}, undefined, [{ name: "database", state: "ok", last_success: last }]);
    const db = within(await screen.findByText("database", { selector: "span.font-mono" }).then((e) => e.closest("li")!));
    expect(db.getByText("OK")).toBeInTheDocument();
    expect(db.getByText("Last copy 10 minutes ago")).toBeInTheDocument();
    expect(db.getByText(/^next /)).toBeInTheDocument();
  });

  it("turns a backup that has a storage on from Edit, with the rest of its settings", async () => {
    const user = userEvent.setup({ delay: null });
    renderBackups({ backups: [{ ...LIST[0], enabled: false }, LIST[1], LIST[2]] });
    backups.updateBackups.mockResolvedValueOnce({ base: "b1", backups: LIST, redeployed: false, warnings: [] });
    await flip(user, "database");
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith("database saved", { description: undefined }));
    expect(backups.updateBackups).toHaveBeenCalledWith("a1", expect.arrayContaining([{ name: "database", enabled: true, storage: "disk" }]), "b0");
    expect(backups.serverStorages).not.toHaveBeenCalled();
  });

  it("can't turn a backup without a storage on from Edit, and leads to the storage picker", async () => {
    const user = userEvent.setup({ delay: null });
    renderBackups();
    await choose(user, "media", "Edit…");
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByRole("switch", { name: "Back up on schedule" })).toBeDisabled();
    await user.click(within(dialog).getByRole("button", { name: "Pick a storage…" }));
    expect(await screen.findByRole("heading", { name: "Turn on media" })).toBeInTheDocument();
  });

  it("sets a backup up with a storage, and turns another off in Edit", async () => {
    const user = userEvent.setup({ delay: null });
    backups.serverStorages.mockResolvedValue(["disk", "offsite"]);
    renderBackups();
    await user.click(await screen.findByRole("button", { name: "Set up media" }));
    const dialog = await screen.findByRole("dialog");
    await user.selectOptions(await within(dialog).findByLabelText("Storage"), "offsite");
    await user.click(within(dialog).getByRole("button", { name: "Turn on" }));
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith("media turned on", { description: undefined }));
    expect(backups.updateBackups).toHaveBeenLastCalledWith(
      "a1",
      [
        { name: "database", enabled: true, storage: "disk" },
        { name: "media", enabled: true, storage: "offsite" },
        { name: "replica", enabled: true, storage: "s3" },
      ],
      "b0",
    );
    expect(within(row("media")).queryByRole("button", { name: "Set up media" })).not.toBeInTheDocument();

    await flip(user, "database");
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith("database saved", { description: undefined }));
    // Each save carries the base the previous one answered with.
    expect(backups.updateBackups).toHaveBeenLastCalledWith(
      "a1",
      [
        { name: "database", storage: "disk" },
        { name: "media", enabled: true, storage: "offsite" },
        { name: "replica", enabled: true, storage: "s3" },
      ],
      "b1",
    );
  });

  it("reports a refused save and its warnings", async () => {
    const user = userEvent.setup({ delay: null });
    renderBackups();
    backups.updateBackups.mockRejectedValueOnce(new Error("backup database: /data is not on a mount"));
    await flip(user, "database");
    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith("Couldn't save the backups", { description: "backup database: /data is not on a mount" }),
    );
    backups.updateBackups.mockResolvedValueOnce({ backups: LIST, redeployed: true, warnings: ["redeploy failed: port in use"] });
    await user.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Save" }));
    await waitFor(() =>
      expect(toast.warning).toHaveBeenCalledWith("database saved", { description: "The app was redeployed.\nredeploy failed: port in use" }),
    );
  });

  it("backs up now and says when nothing changed or a run is refused", async () => {
    const user = userEvent.setup({ delay: null });
    renderBackups();
    backups.runBackup.mockResolvedValueOnce({ unchanged: true, point: null, warnings: [] });
    await choose(user, "database", "Back up now");
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith("database: nothing changed since the last copy", { description: undefined }));
    backups.runBackup.mockResolvedValueOnce({ unchanged: false, point: { id: "p" }, warnings: ["a note"] });
    await choose(user, "database", "Back up now");
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith("database backed up", { description: "a note" }));
    backups.runBackup.mockRejectedValueOnce(new Error("a backup of database is already queued or running"));
    await choose(user, "database", "Back up now");
    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith("Couldn't back up database", { description: "a backup of database is already queued or running" }),
    );
  });

  it("won't start a second run while one is under way, and shows a restore", async () => {
    const user = userEvent.setup({ delay: null });
    let finish: (v: unknown) => void = () => {};
    backups.runBackup.mockReturnValue(new Promise((resolve) => (finish = resolve)));
    const view = renderBackups();
    await choose(user, "database", "Back up now");
    expect(await menuItem(user, "database", "Back up now")).toHaveAttribute("aria-disabled", "true");
    await user.keyboard("{Escape}");
    expect(within(row("database")).getByText("Running")).toBeInTheDocument();
    view.unmount();

    // Leaving the tab doesn't forget the run in flight.
    renderBackups();
    expect(await menuItem(user, "database", "Back up now")).toHaveAttribute("aria-disabled", "true");
    await user.keyboard("{Escape}");
    await act(async () => finish({ unchanged: false, point: { id: "p" }, warnings: [] }));
    await waitFor(() => expect(within(row("database")).queryByText("Running")).not.toBeInTheDocument());
    expect(await menuItem(user, "database", "Back up now")).not.toHaveAttribute("aria-disabled");
    await user.keyboard("{Escape}");

    let end = () => {};
    act(() => {
      end = trackActivity("a1", "database", "restore");
    });
    expect(screen.getByRole("status")).toHaveTextContent("Restoring database: the app is stopped until it is done");
    expect(within(row("database")).getByText("Restoring")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Add backup" })).toBeDisabled();
    expect(await menuItem(user, "database", "Edit…")).toHaveAttribute("aria-disabled", "true");
    // A second restore waits for this one, like the Overview's.
    expect(screen.getByRole("menuitem", { name: "Restore…" })).toHaveAttribute("aria-disabled", "true");
    await user.keyboard("{Escape}");
    act(() => end());
    await waitFor(() => expect(screen.queryByRole("status")).not.toBeInTheDocument());
  });

  it("takes the agent's word that a backup is running", async () => {
    const user = userEvent.setup({ delay: null });
    renderBackups({}, undefined, [{ name: "database", state: "running" }]);
    expect(await menuItem(user, "database", "Back up now")).toHaveAttribute("aria-disabled", "true");
  });

  it("edits a backup and opens its copies", async () => {
    const user = userEvent.setup({ delay: null });
    backups.listRestorePoints.mockResolvedValue([]);
    renderBackups();
    await choose(user, "database", "Edit…");
    await user.selectOptions(screen.getByLabelText("Schedule"), "6h");
    await user.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith("database saved", { description: undefined }));
    expect(backups.updateBackups.mock.calls[0][1][0]).toEqual({ name: "database", enabled: true, storage: "disk", every: "6h" });
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();

    await choose(user, "database", "Restore…");
    expect(await screen.findByText("No copies yet. Back up now to make one.")).toBeInTheDocument();
    expect(backups.listRestorePoints).toHaveBeenCalledWith("a1", "database");
  });

  it("adds a backup", async () => {
    const user = userEvent.setup({ delay: null });
    renderBackups();
    await user.click(await screen.findByRole("button", { name: /Add backup/ }));
    const dialog = await screen.findByRole("dialog");
    await user.selectOptions(within(dialog).getByLabelText("Where the data is"), "volume:data");
    await user.click(within(dialog).getByRole("radio", { name: "SQLite database file" }));
    await user.type(within(dialog).getByLabelText("Database file"), "audit.db");
    await user.selectOptions(await within(dialog).findByLabelText("Storage"), "offsite");
    expect(within(dialog).getByLabelText("Name")).toHaveValue("audit");
    await user.click(within(dialog).getByRole("button", { name: "Add backup" }));
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith("audit added", { description: undefined }));
    expect(backups.updateBackups.mock.calls[0][1].at(-1)).toEqual({
      name: "audit",
      type: "sqlite",
      enabled: true,
      storage: "offsite",
      every: "24h",
      volume: "data",
      path: "audit.db",
    });
  });

  it("keeps every action of a row in its ⋯ menu, with the same rules", async () => {
    const user = userEvent.setup({ delay: null });
    backups.listRestorePoints.mockResolvedValue([]);
    renderBackups();
    const db = within(await screen.findByText("database", { selector: "span.font-mono" }).then((e) => e.closest("li")!));
    expect(db.queryByRole("button", { name: /Back up now|Restore|Edit/ })).not.toBeInTheDocument();

    await user.click(db.getByRole("button", { name: "Actions for database" }));
    expect((await screen.findAllByRole("menuitem")).map((m) => m.textContent)).toEqual(["Back up now", "Restore…", "Edit…"]);
    backups.runBackup.mockResolvedValueOnce({ unchanged: false, point: { id: "p" }, warnings: [] });
    await user.click(screen.getByRole("menuitem", { name: "Back up now" }));
    await waitFor(() => expect(backups.runBackup).toHaveBeenCalledWith("a1", "database"));
    await choose(user, "database", "Restore…");
    expect(await screen.findByText("No copies yet. Back up now to make one.")).toBeInTheDocument();
    await user.keyboard("{Escape}");
    await waitFor(() => expect(db.getByRole("button", { name: "Actions for database" })).toHaveFocus());
    await choose(user, "database", "Edit…");
    expect(await screen.findByRole("heading", { name: "Edit backup database" })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Cancel" }));

    // Without a storage: Set up inline and first in the menu; nothing runs yet.
    expect(within(row("media")).getByRole("button", { name: "Set up media" })).toBeInTheDocument();
    await user.click(within(row("media")).getByRole("button", { name: "Actions for media" }));
    expect((await screen.findAllByRole("menuitem")).map((m) => m.textContent)).toEqual([
      "Set up…",
      "Back up now",
      "Restore…Set up the backup first: its copies are kept in its storage",
      "Edit…",
    ]);
    expect(screen.getByRole("menuitem", { name: "Back up now" })).toHaveAttribute("aria-disabled", "true");
    expect(screen.getByRole("menuitem", { name: /^Restore…/ })).toHaveAttribute("aria-disabled", "true");
    await user.click(screen.getByRole("menuitem", { name: "Set up…" }));
    expect(await screen.findByRole("heading", { name: "Turn on media" })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Cancel" }));

    // A custom backup's menu has only Edit.
    await user.click(within(row("replica")).getByRole("button", { name: "Actions for replica" }));
    expect((await screen.findAllByRole("menuitem")).map((m) => m.textContent)).toEqual(["Edit…"]);
  });

  it("turns a custom backup off and changes its storage from a reduced Edit", async () => {
    const user = userEvent.setup({ delay: null });
    renderBackups();
    await choose(user, "replica", "Edit…");
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText(/Runs its own backup tool/)).toBeInTheDocument();
    expect(within(dialog).queryByLabelText("Schedule")).not.toBeInTheDocument();
    expect(within(dialog).queryByText("Location")).not.toBeInTheDocument();
    await user.click(within(dialog).getByRole("switch", { name: "Run its backup tool" }));
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith("replica saved", { description: undefined }));
    expect(backups.updateBackups.mock.calls[0][1].find((c: { name: string }) => c.name === "replica")).toEqual({ name: "replica", storage: "s3" });

    await choose(user, "replica", "Edit…");
    await user.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Change storage…" }));
    const pick = await screen.findByRole("dialog");
    await user.selectOptions(await within(pick).findByLabelText("Storage"), "offsite");
    await user.click(within(pick).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith("replica storage changed", { description: undefined }));
    expect(backups.updateBackups.mock.calls[1][1].find((c: { name: string }) => c.name === "replica")).toEqual({ name: "replica", storage: "offsite" });
  });

  it("changes a backup's storage from Edit without turning it on or off", async () => {
    const user = userEvent.setup({ delay: null });
    renderBackups();
    await choose(user, "database", "Edit…");
    expect(screen.getByText("disk", { selector: "span.font-mono" })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Change storage…" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByRole("heading", { name: "Storage of database" })).toBeInTheDocument();
    await user.selectOptions(await within(dialog).findByLabelText("Storage"), "offsite");
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith("database storage changed", { description: undefined }));
    expect(backups.updateBackups.mock.calls[0][1][0]).toEqual({ name: "database", enabled: true, storage: "offsite" });
    await waitFor(() => expect(screen.getByRole("button", { name: "Actions for database" })).toHaveFocus());
  });

  it("removes a backup added here", async () => {
    const user = userEvent.setup({ delay: null });
    const added: EffectiveBackup = { ...LIST[1], name: "uploads", volume: "data", service: undefined, paths: ["up"], in_recipe: false, from_recipe: {} };
    renderBackups({ backups: [LIST[0], added] });
    await choose(user, "uploads", "Edit…");
    await user.click(screen.getByRole("button", { name: "Remove backup" }));
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith("uploads removed", { description: undefined }));
    expect(backups.updateBackups.mock.calls[0][1]).toEqual([{ name: "database", enabled: true, storage: "disk" }]);
  });

  it("reloads and says so when the backups were changed elsewhere", async () => {
    const user = userEvent.setup({ delay: null });
    const getApp = vi.fn().mockResolvedValue({ backups: LIST, backup_targets: null, backups_base: "b0" });
    backups.updateBackups.mockRejectedValueOnce(new Error("the backups changed since you opened them; reload and try again"));
    renderBackups({}, getApp);
    await flip(user, "database");
    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith("The backups were changed elsewhere", {
        description: "the backups changed since you opened them; reload and try again",
      }),
    );
    await waitFor(() => expect(getApp).toHaveBeenCalledTimes(2));
  });

  it("changes nothing while the backups can't be read", async () => {
    renderBackups({ backups: [], backups_error: "x-winterflow.backups[0].volume: \"gone\" is not a volume" });
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent('The backups can\'t be read: x-winterflow.backups[0].volume: "gone" is not a volume');
    expect(alert).toHaveTextContent("roll back in History");
    expect(screen.getByRole("button", { name: /Add backup/ })).toBeDisabled();
    expect(screen.queryByText(/This app has no backups/)).not.toBeInTheDocument();
    expect(backups.updateBackups).not.toHaveBeenCalled();
  });

  it("shows an empty state and a load failure with a retry", async () => {
    const user = userEvent.setup({ delay: null });
    renderBackups({ backups: [] });
    expect(await screen.findByText(/This app has no backups/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Add backup/ })).toBeEnabled();

    const getApp = vi.fn().mockRejectedValueOnce(new Error("agent offline")).mockResolvedValue({ backups: LIST });
    renderBackups({}, getApp);
    await user.click(await screen.findByRole("button", { name: "Try again" }));
    await waitFor(() => expect(getApp).toHaveBeenCalledTimes(2));
    expect(await screen.findByRole("button", { name: "Actions for database" })).toBeInTheDocument();
  });

  // mount renders the tab with a health snapshot that can be replaced, like
  // the status pushes do.
  function mount(health: BackupHealth[], list: EffectiveBackup[] = LIST) {
    const getApp = vi.fn().mockResolvedValue({ backups: list, backup_targets: { volumes: ["data"], services: [] }, retention_days: 180, backups_base: "b0" });
    const ui = (h: BackupHealth[]) => (
      <MemoryRouter>
        <AppsContext.Provider value={{ getApp, backupsByApp: { a1: h } } as unknown as AppsContextValue}>
          <AppBackups appId="a1" />
        </AppsContext.Provider>
      </MemoryRouter>
    );
    const view = render(ui(health));
    return { getApp, setHealth: (h: BackupHealth[]) => view.rerender(ui(h)) };
  }
  const nowSecs = () => Math.floor(Date.now() / 1000);

  it("keeps showing a finished copy until a newer report arrives, and never falls back from Running", async () => {
    const user = userEvent.setup({ delay: null });
    let finish: (v: unknown) => void = () => {};
    backups.runBackup.mockReturnValue(new Promise((resolve) => (finish = resolve)));
    const old = nowSecs() - 3600;
    const view = mount([{ name: "database", state: "ok", last_success: old }]);
    await choose(user, "database", "Back up now");
    expect(within(row("database")).getByText("Running")).toBeInTheDocument();
    await act(async () => finish({ unchanged: false, point: { id: "p" }, warnings: [] }));
    // The told result, not the older report (Not run yet).
    expect(within(row("database")).getByText("OK")).toBeInTheDocument();
    expect(within(row("database")).getByText(/^Last copy (now|\d+ seconds? ago)$/)).toBeInTheDocument();
    expect(within(row("database")).queryByText("Last copy 1 hour ago")).not.toBeInTheDocument();
    // An older report (or one from before the copy) changes nothing.
    view.setHealth([{ name: "database", state: "ok", last_success: old }]);
    expect(within(row("database")).getByText(/^Last copy (now|\d+ seconds? ago)$/)).toBeInTheDocument();
    view.setHealth([{ name: "database", state: "running" }]);
    expect(within(row("database")).getByText("OK")).toBeInTheDocument();
    // A report that shows the copy takes over.
    view.setHealth([{ name: "database", state: "ok", last_success: nowSecs() + 5 }]);
    expect(within(row("database")).getByText("OK")).toBeInTheDocument();
    // And a report is followed again from then on.
    view.setHealth([{ name: "database", state: "failing", last_success: nowSecs() + 5, last_error: "disk full" }]);
    expect(within(row("database")).getByText("Failing")).toBeInTheDocument();
  });

  it("shows a failed run as failing at once, in a line, with the full text on demand", async () => {
    const user = userEvent.setup({ delay: null });
    const full = "upload the copy: open /home/d/backups/disk/01a1/files/.wf-tmp-2637751049: permission denied";
    backups.runBackup.mockRejectedValue(new AgentError(full));
    const last = nowSecs() - 60;
    const view = mount([{ name: "database", state: "ok", last_success: last }]);
    await choose(user, "database", "Back up now");
    const db = within(row("database"));
    await waitFor(() => expect(db.getByText("Failing")).toBeInTheDocument());
    expect(db.getByText("upload the copy: permission denied")).toBeVisible();
    expect(db.getByText(full)).not.toBeVisible();
    await user.click(db.getByText("Details"));
    expect(db.getByText(full)).toBeVisible();
    // The report from before the failure (OK) does not undo it; the one that
    // shows it does.
    view.setHealth([{ name: "database", state: "ok", last_success: last }]);
    expect(within(row("database")).getByText("Failing")).toBeInTheDocument();
    view.setHealth([{ name: "database", state: "failing", last_success: last, last_error: "disk full" }]);
    expect(within(row("database")).getByText("Failing")).toBeInTheDocument();
    expect(within(row("database")).getByText("disk full")).toBeInTheDocument();
  });

  it("doesn't call a refused or unreachable run a failure of the backup", async () => {
    const user = userEvent.setup({ delay: null });
    mount([{ name: "database", state: "ok", last_success: nowSecs() - 60 }]);
    backups.runBackup.mockRejectedValueOnce(new AgentError("a backup of database is already queued or running"));
    await choose(user, "database", "Back up now");
    await waitFor(() => expect(toast.error).toHaveBeenCalled());
    backups.runBackup.mockRejectedValueOnce(new Error("Request failed: 502"));
    await choose(user, "database", "Back up now");
    await waitFor(() => expect(toast.error).toHaveBeenCalledTimes(2));
    expect(within(row("database")).getByText("OK")).toBeInTheDocument();
  });

  it("shows a backup turned off as Off until a report says so, and turned on as Not run yet with when the first copy comes", async () => {
    const user = userEvent.setup({ delay: null });
    const view = mount([{ name: "database", state: "failing", last_error: "disk full" }]);
    await flip(user, "database");
    await waitFor(() => expect(within(row("database")).getByText("Off")).toBeInTheDocument());
    expect(within(row("database")).queryByText("disk full")).not.toBeInTheDocument();
    view.setHealth([{ name: "database", state: "failing", last_error: "disk full" }]); // older
    expect(within(row("database")).getByText("Off")).toBeInTheDocument();
    view.setHealth([]);
    expect(within(row("database")).getByText("Off")).toBeInTheDocument();

    // Turned on again (it has its storage): the old Off report must not
    // contradict it.
    await flip(user, "database");
    await waitFor(() => expect(toast.success).toHaveBeenCalledTimes(2));
    expect(within(row("database")).getByText("Not run yet")).toBeInTheDocument();
    expect(within(row("database")).getByText("next within 1 hour")).toBeInTheDocument();
    view.setHealth([{ name: "database", state: "off" }]);
    expect(within(row("database")).getByText("Not run yet")).toBeInTheDocument();
    view.setHealth([{ name: "database", state: "ok", last_success: nowSecs() }]);
    expect(within(row("database")).getByText("OK")).toBeInTheDocument();
    expect(within(row("database")).getByText(/^next \d/)).toBeInTheDocument();
  });

  it("gives up a held state after a while", async () => {
    mount([{ name: "database", state: "ok", last_success: nowSecs() - 3600 }]);
    await screen.findByRole("button", { name: "Actions for database" });
    act(() => holdHealth("a1", "database", { kind: "off" }, undefined, Date.now() - OVERLAY_HOLD_MS + 150));
    expect(within(row("database")).getByText("Off")).toBeInTheDocument();
    await waitFor(() => expect(within(row("database")).getByText("OK")).toBeInTheDocument());
  });

  it("keeps a refused save in the dialog, not only in a toast", async () => {
    const user = userEvent.setup({ delay: null });
    mount(HEALTH);
    backups.updateBackups.mockRejectedValueOnce(new Error("service web: /tmp/stuff is not on a mount: mount a volume there"));
    await choose(user, "database", "Edit…");
    await user.selectOptions(screen.getByLabelText("Schedule"), "6h");
    await user.click(screen.getByRole("button", { name: "Save" }));
    const dialog = await screen.findByRole("dialog");
    await waitFor(() => expect(within(dialog).getByRole("alert")).toHaveTextContent("/tmp/stuff is not on a mount"));
    expect(toast.error).toHaveBeenCalledWith("Couldn't save the backups", { description: "service web: /tmp/stuff is not on a mount: mount a volume there" });
    // The next save clears it.
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith("database saved", { description: undefined }));

    backups.serverStorages.mockResolvedValue(["disk"]);
    backups.updateBackups.mockRejectedValueOnce(new Error("storage disk is gone"));
    await user.click(screen.getByRole("button", { name: "Set up media" }));
    const enable = await screen.findByRole("dialog");
    await user.click(await within(enable).findByRole("button", { name: "Turn on" }));
    await waitFor(() => expect(within(enable).getByRole("alert")).toHaveTextContent("storage disk is gone"));
    await user.click(within(enable).getByRole("button", { name: "Cancel" }));

    backups.updateBackups.mockRejectedValueOnce(new Error("backups[3].volume: nope"));
    await user.click(screen.getByRole("button", { name: /Add backup/ }));
    const add = await screen.findByRole("dialog");
    await user.selectOptions(within(add).getByLabelText("Where the data is"), "volume:data");
    await user.click(within(add).getByRole("radio", { name: "SQLite database file" }));
    await user.type(within(add).getByLabelText("Database file"), "audit.db");
    await user.selectOptions(await within(add).findByLabelText("Storage"), "disk");
    await user.click(within(add).getByRole("button", { name: "Add backup" }));
    await waitFor(() => expect(within(add).getByText("backups[3].volume: nope")).toBeInTheDocument());
  });

  it("gives focus back to the control that opened a dialog", async () => {
    const user = userEvent.setup({ delay: null });
    backups.listRestorePoints.mockResolvedValue([]);
    backups.serverStorages.mockResolvedValue(["disk"]);
    mount(HEALTH);
    await choose(user, "database", "Restore…");
    await screen.findByRole("dialog");
    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.getByRole("button", { name: "Actions for database" })).toHaveFocus());

    await choose(user, "database", "Edit…");
    await user.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.getByRole("button", { name: "Actions for database" })).toHaveFocus());

    await user.click(screen.getByRole("button", { name: "Set up media" }));
    await user.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.getByRole("button", { name: "Set up media" })).toHaveFocus());

    await user.click(screen.getByRole("button", { name: /Add backup/ }));
    await user.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.getByRole("button", { name: /Add backup/ })).toHaveFocus());
  });

  it("tells a stale tab to reload and reloads it when the compose file changed under it", async () => {
    const user = userEvent.setup({ delay: null });
    const { getApp } = mount(HEALTH);
    backups.updateBackups.mockRejectedValueOnce(new Error("backups[1]: media is declared by the compose file; edit it instead of adding it"));
    await flip(user, "database");
    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith("The backups were changed elsewhere", {
        description: expect.stringContaining("This page was out of date; it was reloaded, so try again."),
      }),
    );
    await waitFor(() => expect(getApp).toHaveBeenCalledTimes(2));
    expect(toast.error).not.toHaveBeenCalledWith("Couldn't save the backups", expect.anything());
  });

  it("shows the intro only when there are backups, and the empty state on its own", async () => {
    mount([]);
    expect(await screen.findByText(/Backups copy this app's data to one of the server's storages on a schedule\. Backups the compose file declares are listed ready to set up\./)).toBeInTheDocument();
    cleanup();
    mount([], []);
    expect(await screen.findByText(/This app has no backups yet/)).toBeInTheDocument();
    expect(screen.queryByText(/Backups copy this app's data to one of the server's storages on a schedule\. Backups the compose file declares are listed ready to set up\./)).not.toBeInTheDocument();
  });
});
