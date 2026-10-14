import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { toast } from "sonner";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));
vi.mock("@/lib/ecies", () => ({
  encryptSecret: vi.fn(async (value: string, key: string) => `ct(${key}):${value}`),
}));
const active = vi.hoisted(() => ({
  server: null as { id: string; name: string } | null,
  status: {} as Record<string, "online" | "unknown">,
  // The org's servers: two unless a test says otherwise.
  servers: [{ id: "srv-a" }, { id: "srv-b" }] as { id: string }[],
  loaded: true,
}));
vi.mock("@/context/use-servers", () => ({
  useServers: () => ({ activeServer: active.server, statusByServer: active.status, servers: active.servers, loaded: active.loaded }),
}));

import { AppsContext, type AppsContextValue } from "@/context/apps-context-base";
import {
  NotificationsContext,
  type Notification,
  type NotificationHandler,
  type NotificationsContextValue,
} from "@/context/notifications-context-base";
import type { Rollout } from "@/lib/rollouts";
import type { EncryptionKeys, OrgVariables, ServerVariables, SharedVariablesApi } from "@/lib/shared-variables";
import SharedVariablesSettingsPage from "@/pages/settings-shared-variables";
import { rollout } from "@/test/rollouts";

const T = "2026-10-11T00:00:00Z";
const ORG: OrgVariables = {
  variables: [
    { id: "o1", name: "SMTP_HOST", scope: "org", is_secret: false, value: "mail", description: "Mail relay", updated_at: T, updated_by: "u", missing_on: [] },
    { id: "o2", name: "SMTP_PASS", scope: "org", is_secret: true, value: "<encrypted>", description: "", updated_at: T, updated_by: "u", missing_on: ["srv-a"] },
  ],
  servers: [
    { server_id: "srv-a", name: "alpha", has_key: true, revision: 3, agent_revision: 2, state: "pending" },
    { server_id: "srv-b", name: "beta", has_key: true, revision: 3, agent_revision: 3, state: "synced" },
  ],
};
const SERVER: ServerVariables = {
  variables: [{ id: "s1", name: "SMTP_HOST", scope: "server", is_secret: false, value: "local", description: "", updated_at: T, updated_by: "u", missing_on: [] }],
  inherited: [
    { ...ORG.variables[0], overridden: true },
    { ...ORG.variables[1], overridden: false },
  ],
  builtins: [{ name: "WF_SERVER_NAME", value: "alpha", description: "This server's name" }],
  missing_org_secrets: ["SMTP_PASS"],
  sync: { server_id: "srv-a", name: "alpha", has_key: true, revision: 3, agent_revision: 2, state: "pending" },
};
const KEYS: EncryptionKeys = {
  servers: [
    { server_id: "srv-a", name: "alpha", public_key: "KA" },
    { server_id: "srv-b", name: "beta", public_key: "KB" },
  ],
  pending_servers: [],
};

function fakeApi() {
  return {
    listOrg: vi.fn().mockResolvedValue(ORG),
    listServer: vi.fn().mockResolvedValue(SERVER),
    keys: vi.fn().mockResolvedValue(KEYS),
    saveOrg: vi.fn().mockResolvedValue({}),
    saveServer: vi.fn().mockResolvedValue({}),
    deleteOrg: vi.fn().mockResolvedValue(undefined),
    deleteServer: vi.fn().mockResolvedValue(undefined),
    resync: vi.fn().mockResolvedValue([]),
    reload: vi.fn().mockResolvedValue([]),
  };
}

// a4 is stale but stopped: the agent skips it, so it is not counted.
const STALE = { a1: ["SMTP_PASS"], a2: ["SMTP_HOST"], a3: [], a4: ["SMTP_HOST"] };

function renderPage(
  api: ReturnType<typeof fakeApi>,
  opts: { standalone?: boolean; stale?: Record<string, string[]>; running?: Rollout[] } = {},
) {
  const rollouts = { list: vi.fn().mockResolvedValue(opts.running ?? []), stop: vi.fn(), upgradeAll: vi.fn() };
  // Every subscriber hears each event (the page and its rollout strip).
  const handlers = new Set<NotificationHandler>();
  const notifications: NotificationsContextValue = {
    connected: true,
    subscribe: (h) => {
      handlers.add(h);
      return () => {
        handlers.delete(h);
      };
    },
    waitFor: vi.fn(),
  };
  const emit = (n: Notification) => act(() => handlers.forEach((h) => h(n)));
  const apps = {
    staleVarsByApp: opts.stale ?? STALE,
    statusByApp: { a1: 1, a2: 2, a3: 1, a4: 5 },
  } as unknown as AppsContextValue;
  render(
    <MemoryRouter>
      <NotificationsContext.Provider value={notifications}>
        <AppsContext.Provider value={apps}>
          <SharedVariablesSettingsPage
            api={api as unknown as SharedVariablesApi}
            rollouts={rollouts}
            standalone={opts.standalone ?? false}
          />
        </AppsContext.Provider>
      </NotificationsContext.Provider>
    </MemoryRouter>,
  );
  return { emit };
}

const setupUser = () => userEvent.setup({ delay: null });
const listRows = async () =>
  within(await screen.findByRole("list", { name: "Shared variables" })).getAllByRole("listitem");

describe("SharedVariablesSettingsPage", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    active.server = { id: "srv-a", name: "alpha" };
    active.status = { "srv-a": "unknown", "srv-b": "online" };
  });

  it("lists the organization's and this server's variables in one list, with their scope", async () => {
    renderPage(fakeApi());
    expect(screen.getByRole("heading", { level: 1, name: "Shared variables" })).toBeInTheDocument();
    const rows = await listRows();
    expect(rows.map((r) => within(r).getByText(/^SMTP_/).textContent)).toEqual(["SMTP_HOST", "SMTP_HOST", "SMTP_PASS"]);
    expect(rows[0]).toHaveTextContent("All servers");
    expect(rows[0]).toHaveTextContent("mail");
    expect(rows[0]).toHaveTextContent("Mail relay");
    expect(rows[0]).toHaveTextContent("alpha uses its own value");
    expect(rows[1]).toHaveTextContent("alpha only");
    expect(rows[1]).toHaveTextContent("local");
    expect(rows[2]).toHaveTextContent("Secret");
    expect(rows[2]).toHaveTextContent("Missing on alpha");
    // Status in words and a dot, not coloured text.
    expect(within(rows[2]).getByText(/Missing on alpha/)).toHaveClass("text-foreground");
  });

  it("never prints a secret's value", async () => {
    renderPage(fakeApi());
    await listRows();
    expect(screen.queryByText(/<encrypted>/)).not.toBeInTheDocument();
  });

  it("folds the built-in variables away, read-only", async () => {
    const user = setupUser();
    renderPage(fakeApi());
    const toggle = await screen.findByRole("button", { name: "Built-in variables (1)" });
    expect(screen.queryByText("WF_SERVER_NAME")).not.toBeInTheDocument();
    await user.click(toggle);
    const builtins = screen.getByRole("list", { name: "Built-in variables" });
    expect(within(builtins).getByText("WF_SERVER_NAME")).toBeInTheDocument();
    expect(within(builtins).queryByRole("button")).not.toBeInTheDocument();
  });

  it("says a sync is pending, or failed, and sends again", async () => {
    const user = setupUser();
    const api = fakeApi();
    const { emit } = renderPage(api);
    await listRows();
    // alpha is keyed and still pending: the notice offers Send again before any failure arrives.
    const pending = screen.getByRole("status", { name: "Sync" });
    expect(pending).toHaveTextContent("Sync to alpha is pending.");
    emit({ type: "vars.sync_failed", ref: "", payload: { server_id: "srv-b", error: "boom" } });
    const banner = screen.getByRole("alert", { name: "Sync" });
    expect(banner).toHaveTextContent("Sync to beta failed.");
    expect(banner).toHaveTextContent("Sync to alpha is pending.");
    await user.click(within(banner).getByRole("button", { name: "Send again" }));
    expect(api.resync).toHaveBeenCalledWith({});
    // Sent: nothing is failed any more, both servers read pending until they answer.
    expect(screen.queryByRole("alert", { name: "Sync" })).not.toBeInTheDocument();
  });

  it("keeps quiet about a pending sync while its server is online, until it has lasted a minute", async () => {
    vi.useFakeTimers({ toFake: ["Date", "setInterval", "clearInterval"] });
    try {
      active.status = { "srv-a": "online", "srv-b": "online" };
      renderPage(fakeApi());
      await act(async () => {
        await vi.advanceTimersByTimeAsync(0);
      });
      expect(screen.getByRole("list", { name: "Shared variables" })).toBeInTheDocument();
      expect(screen.queryByRole("status", { name: "Sync" })).not.toBeInTheDocument();
      await act(async () => {
        await vi.advanceTimersByTimeAsync(50_000);
      });
      expect(screen.queryByRole("status", { name: "Sync" })).not.toBeInTheDocument();
      await act(async () => {
        await vi.advanceTimersByTimeAsync(20_000);
      });
      expect(screen.getByRole("status", { name: "Sync" })).toHaveTextContent("Sync to alpha is pending.");
    } finally {
      vi.useRealTimers();
    }
  });

  it("says a failed sync at once, even when the server is online", async () => {
    active.status = { "srv-a": "online", "srv-b": "online" };
    const { emit } = renderPage(fakeApi());
    await listRows();
    expect(screen.queryByRole("status", { name: "Sync" })).not.toBeInTheDocument();
    emit({ type: "vars.sync_failed", ref: "", payload: { server_id: "srv-b", error: "boom" } });
    expect(screen.getByRole("alert", { name: "Sync" })).toHaveTextContent("Sync to beta failed.");
  });

  it("has no sync notice once every keyed server is synced", async () => {
    const api = fakeApi();
    api.listOrg.mockResolvedValue({ ...ORG, servers: ORG.servers.map((s) => ({ ...s, state: "synced" as const })) });
    api.listServer.mockResolvedValue({ ...SERVER, sync: { ...SERVER.sync, state: "synced" as const } });
    renderPage(api);
    await listRows();
    expect(screen.queryByRole("status", { name: "Sync" })).not.toBeInTheDocument();
    expect(screen.queryByRole("alert", { name: "Sync" })).not.toBeInTheDocument();
  });

  it("keeps a page-level Send to servers again for every keyed server", async () => {
    const user = setupUser();
    const api = fakeApi();
    renderPage(api);
    await listRows();
    await user.click(screen.getByRole("button", { name: "More actions for shared variables" }));
    await user.click(await screen.findByRole("menuitem", { name: "Send to servers again" }));
    expect(api.resync).toHaveBeenCalledWith({});
  });

  it("offers Reload while apps use old values, and only then", async () => {
    const user = setupUser();
    const api = fakeApi();
    renderPage(api);
    const banner = await screen.findByRole("status", { name: "Old values" });
    expect(banner).toHaveTextContent("2 apps on alpha use old values.");
    await user.click(within(banner).getByRole("button", { name: "Reload" }));
    expect(api.reload).toHaveBeenCalledWith({ server_id: "srv-a" });
  });

  it("has no reload banner when nothing is stale", async () => {
    renderPage(fakeApi(), { stale: {} });
    await listRows();
    expect(screen.queryByRole("status", { name: "Old values" })).not.toBeInTheDocument();
  });

  it("asks for an org secret this server lacks and encrypts it for this server only", async () => {
    const user = setupUser();
    const api = fakeApi();
    renderPage(api);
    const banner = await screen.findByRole("status", { name: "Missing secrets" });
    expect(banner).toHaveTextContent("1 secret isn't on alpha yet.");
    await user.click(within(banner).getByRole("button", { name: "Provide value" }));
    const dialog = await screen.findByRole("dialog");
    expect(dialog).toHaveTextContent("Encrypts the value for: alpha");
    await user.type(within(dialog).getByLabelText("Value"), "s3cret");
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() =>
      expect(api.saveOrg).toHaveBeenCalledWith({
        name: "SMTP_PASS", is_secret: true, description: "", ciphertexts: { "srv-a": "ct(KA):s3cret" },
      }),
    );
    await waitFor(() => expect(api.listServer).toHaveBeenCalledTimes(2)); // listed again after the save
  });

  it("adds an org-wide variable by default and a server-only one under Advanced", async () => {
    const user = setupUser();
    const api = fakeApi();
    renderPage(api);
    await listRows();
    await user.click(screen.getByRole("button", { name: /Add variable/ }));
    let dialog = await screen.findByRole("dialog");
    await user.type(within(dialog).getByLabelText("Name"), "TZ");
    await user.type(within(dialog).getByLabelText("Value"), "UTC");
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(api.saveOrg).toHaveBeenCalledWith({ name: "TZ", is_secret: false, value: "UTC", description: "" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());

    await user.click(screen.getByRole("button", { name: /Add variable/ }));
    dialog = await screen.findByRole("dialog");
    await user.type(within(dialog).getByLabelText("Name"), "LOCAL_DIR");
    await user.type(within(dialog).getByLabelText("Value"), "/srv");
    await user.click(within(dialog).getByRole("button", { name: "Advanced" }));
    await user.click(within(dialog).getByRole("radio", { name: "alpha only" }));
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() =>
      expect(api.saveServer).toHaveBeenCalledWith("srv-a", { name: "LOCAL_DIR", is_secret: false, value: "/srv", description: "" }),
    );
  });

  it("drops the words about several servers in an org with one server", async () => {
    const user = setupUser();
    const api = fakeApi();
    active.servers = [{ id: "srv-a" }];
    try {
      renderPage(api);
      const rows = await listRows();
      expect(screen.getByRole("list", { name: "Shared variables" })).not.toHaveTextContent("All servers");
      // The server's own variable still says so.
      expect(rows[1]).toHaveTextContent("alpha only");
      expect(screen.queryByRole("button", { name: "More actions for shared variables" })).not.toBeInTheDocument();
      // Deleting an org variable removes it from this one server.
      await user.click(within(rows[0]).getByRole("button", { name: /^Actions for / }));
      await user.click(await screen.findByRole("menuitem", { name: "Delete" }));
      expect(await screen.findByRole("alertdialog")).toHaveTextContent(
        "It's removed from this server. Apps that use it lose the value on their next deploy or reload.",
      );
      await user.click(screen.getByRole("button", { name: "Cancel" }));
      await user.click(within(rows[0]).getByRole("button", { name: /^Actions for / }));
      expect(screen.queryByRole("menuitem", { name: "Send to servers again" })).not.toBeInTheDocument();
      await user.keyboard("{Escape}");
      await user.click(screen.getByRole("button", { name: /Add variable/ }));
      const dialog = await screen.findByRole("dialog");
      await user.click(within(dialog).getByRole("switch", { name: "Secret" }));
      await user.type(within(dialog).getByLabelText("Value"), "s3cret");
      expect(dialog).not.toHaveTextContent(/pushed/);
      expect(within(dialog).getByRole("button", { name: "Save" })).toBeInTheDocument();
      expect(within(dialog).queryByRole("button", { name: "Save and push" })).not.toBeInTheDocument();
      await user.click(within(dialog).getByRole("button", { name: "Advanced" }));
      expect(within(dialog).queryByRole("radiogroup", { name: "Applies to" })).not.toBeInTheDocument();
    } finally {
      active.servers = [{ id: "srv-a" }, { id: "srv-b" }];
    }
  });

  it("keeps the words about several servers until the servers are read", async () => {
    active.servers = [];
    active.loaded = false;
    try {
      renderPage(fakeApi());
      await listRows();
      expect(screen.getByRole("list", { name: "Shared variables" })).toHaveTextContent("All servers");
    } finally {
      active.servers = [{ id: "srv-a" }, { id: "srv-b" }];
      active.loaded = true;
    }
  });

  it("offers Try again, not Send again, after a failed sync in a standalone install", async () => {
    const user = setupUser();
    const api = fakeApi();
    renderPage(api, { standalone: true });
    await listRows();
    await user.click(within(screen.getByRole("status", { name: "Sync" })).getByRole("button", { name: "Try again" }));
    expect(api.resync).toHaveBeenCalled();
  });

  it("offers no scope in a standalone install, and edits a server-only variable in its own scope", async () => {
    const user = setupUser();
    const api = fakeApi();
    renderPage(api, { standalone: true });
    await listRows();
    await user.click(screen.getByRole("button", { name: /Add variable/ }));
    const dialog = await screen.findByRole("dialog");
    await user.click(within(dialog).getByRole("button", { name: "Advanced" }));
    expect(within(dialog).queryByRole("radiogroup", { name: "Applies to" })).not.toBeInTheDocument();
    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());

    const rows = await listRows();
    await user.click(within(rows[1]).getByRole("button", { name: "Actions for SMTP_HOST" }));
    await user.click(await screen.findByRole("menuitem", { name: "Edit" }));
    const edit = await screen.findByRole("dialog");
    await user.clear(within(edit).getByLabelText("Value"));
    await user.type(within(edit).getByLabelText("Value"), "other");
    await user.click(within(edit).getByRole("button", { name: "Save" }));
    await waitFor(() =>
      expect(api.saveServer).toHaveBeenCalledWith("srv-a", { name: "SMTP_HOST", is_secret: false, value: "other", description: "" }),
    );
  });

  it("deletes in the variable's own scope after a confirmation", async () => {
    const user = setupUser();
    const api = fakeApi();
    renderPage(api);
    const rows = await listRows();
    await user.click(within(rows[1]).getByRole("button", { name: "Actions for SMTP_HOST" }));
    await user.click(await screen.findByRole("menuitem", { name: "Delete" }));
    const confirm = await screen.findByRole("alertdialog");
    expect(confirm).toHaveTextContent("fall back to the organization's value");
    await user.click(within(confirm).getByRole("button", { name: "Delete variable" }));
    await waitFor(() => expect(api.deleteServer).toHaveBeenCalledWith("srv-a", "s1"));
    expect(api.deleteOrg).not.toHaveBeenCalled();
  });

  it("reloads and sends one variable again from its menu", async () => {
    const user = setupUser();
    const api = fakeApi();
    renderPage(api);
    const rows = await listRows();
    const menu = async (row: HTMLElement, item: string) => {
      await user.click(within(row).getByRole("button", { name: "Actions for SMTP_HOST" }));
      await user.click(await screen.findByRole("menuitem", { name: item }));
    };
    await menu(rows[0], "Reload apps using it");
    expect(api.reload).toHaveBeenCalledWith({ variable_id: "o1" });
    await menu(rows[0], "Send to servers again");
    expect(api.resync).toHaveBeenCalledWith({ variable_id: "o1" });
    await menu(rows[1], "Reload apps using it");
    expect(api.reload).toHaveBeenCalledWith({ server_id: "srv-a", names: ["SMTP_HOST"] });
    await user.click(within(rows[1]).getByRole("button", { name: "Actions for SMTP_HOST" }));
    expect(screen.queryByRole("menuitem", { name: "Send to servers again" })).not.toBeInTheDocument();
  });

  it("shows the org's reload rollout", async () => {
    renderPage(fakeApi(), {
      running: [rollout({ kind: "vars_reload", done: 0, skipped: 0, total: 2, current_app_name: "shop", current_server_id: "srv-b" })],
    });
    expect(await screen.findByText("Reloading 1 of 2: shop on beta…")).toBeInTheDocument();
  });

  it("shows only the organization's without a server", async () => {
    active.server = null;
    const api = fakeApi();
    renderPage(api);
    const rows = await listRows();
    expect(rows).toHaveLength(2);
    expect(api.listServer).not.toHaveBeenCalled();
    expect(screen.queryByRole("button", { name: /Built-in variables/ })).not.toBeInTheDocument();
    expect(screen.queryByRole("status", { name: "Old values" })).not.toBeInTheDocument();
  });
  it("returns focus to Add variable after a variable is deleted, and to the opener on a refusal", async () => {
    const user = setupUser();
    const api = fakeApi();
    api.deleteServer.mockRejectedValueOnce(new Error("busy"));
    renderPage(api);
    const rows = await listRows();
    const menuButton = within(rows[1]).getByRole("button", { name: "Actions for SMTP_HOST" });
    await user.click(menuButton);
    await user.click(await screen.findByRole("menuitem", { name: "Delete" }));
    let confirm = await screen.findByRole("alertdialog");
    await user.click(within(confirm).getByRole("button", { name: "Delete variable" }));
    // Refused: the confirmation stays open; cancelling returns to the row's menu.
    await waitFor(() => expect(toast.error).toHaveBeenCalledWith("Couldn't delete the variable", { description: "busy" }));
    expect(screen.getByRole("alertdialog")).toBeInTheDocument();
    await user.click(within(screen.getByRole("alertdialog")).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(menuButton).toHaveFocus());

    // The next delete goes through: the row (and its menu) is gone from the list.
    api.listServer.mockResolvedValue({ ...SERVER, variables: [] });
    await user.click(menuButton);
    await user.click(await screen.findByRole("menuitem", { name: "Delete" }));
    confirm = await screen.findByRole("alertdialog");
    await user.click(within(confirm).getByRole("button", { name: "Delete variable" }));
    await waitFor(() => expect(api.deleteServer).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument());
    await waitFor(() => expect(screen.getByRole("button", { name: /Add variable/ })).toHaveFocus());
  });

  it("returns focus to Add variable when its dialog closes", async () => {
    const user = setupUser();
    renderPage(fakeApi());
    await listRows();
    const add = screen.getByRole("button", { name: /Add variable/ });
    await user.click(add);
    await user.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    await waitFor(() => expect(add).toHaveFocus());
  });
});
