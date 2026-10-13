import { act, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { toast } from "sonner";
import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));
vi.mock("@/lib/ecies", () => ({
  encryptSecret: vi.fn(async (value: string, key: string) => `ct(${key}):${value}`),
}));

import { AppsContext, type AppsContextValue } from "@/context/apps-context-base";
import {
  NotificationsContext,
  type Notification,
  type NotificationHandler,
  type NotificationsContextValue,
} from "@/context/notifications-context-base";
import type { Rollout } from "@/lib/rollouts";
import type { EncryptionKeys, ServerVariables, SharedVariablesApi } from "@/lib/shared-variables";
import { VariablesCard } from "@/pages/settings-variables";
import { rollout } from "@/test/rollouts";

const T = "2026-10-11T00:00:00Z";
const SERVER: ServerVariables = {
  variables: [{ id: "s1", name: "SMTP_HOST", scope: "server", is_secret: false, value: "local", description: "", updated_at: T, updated_by: "u", missing_on: [] }],
  inherited: [
    { id: "o1", name: "SMTP_HOST", scope: "org", is_secret: false, value: "org-mail", description: "", updated_at: T, updated_by: "u", missing_on: [], overridden: true },
    { id: "o2", name: "SMTP_PASS", scope: "org", is_secret: true, value: "<encrypted>", description: "", updated_at: T, updated_by: "u", missing_on: ["srv-a"], overridden: false },
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
    listOrg: vi.fn(),
    listServer: vi.fn().mockResolvedValue(SERVER),
    keys: vi.fn().mockResolvedValue(KEYS),
    saveOrg: vi.fn().mockResolvedValue({}),
    saveServer: vi.fn().mockResolvedValue({}),
    deleteOrg: vi.fn(),
    deleteServer: vi.fn().mockResolvedValue(undefined),
    resync: vi.fn().mockResolvedValue(["srv-a"]),
    reload: vi.fn().mockResolvedValue(["srv-a"]),
  };
}

function renderCard(api: ReturnType<typeof fakeApi>, serverId = "srv-a", running: Rollout[] = []) {
  const rollouts = { list: vi.fn().mockResolvedValue(running), stop: vi.fn(), upgradeAll: vi.fn() };
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
  // a4 is stale but stopped: the agent skips it, so it is not counted.
  const apps = {
    staleVarsByApp: { a1: ["SMTP_PASS"], a2: ["SMTP_HOST"], a3: [], a4: ["SMTP_HOST"] },
    statusByApp: { a1: 1, a2: 2, a3: 1, a4: 5 },
  } as unknown as AppsContextValue;
  const tree = (id: string) => (
    <MemoryRouter>
      <NotificationsContext.Provider value={notifications}>
        <AppsContext.Provider value={apps}>
          <VariablesCard serverId={id} api={api as unknown as SharedVariablesApi} rollouts={rollouts} />
        </AppsContext.Provider>
      </NotificationsContext.Provider>
    </MemoryRouter>
  );
  const view = render(tree(serverId));
  return { switchTo: (id: string) => view.rerender(tree(id)), emit };
}

describe("VariablesCard", () => {
  beforeEach(() => vi.clearAllMocks());

  it("shows this server's, the inherited and the built-in variables", async () => {
    renderCard(fakeApi());
    expect(await screen.findByText("local")).toBeInTheDocument();
    const org = screen.getByRole("region", { name: "Inherited from org" });
    expect(within(org).getByText("overridden")).toBeInTheDocument();
    expect(within(screen.getByRole("region", { name: "Built-in" })).getByText("alpha")).toBeInTheDocument();
    expect(screen.getByText("Sync pending")).toBeInTheDocument();
  });

  it("follows the sync state over SSE", async () => {
    const { emit } = renderCard(fakeApi());
    await screen.findByText("Sync pending");
    emit({ type: "vars.sync_failed", ref: "", payload: { server_id: "srv-a", error: "boom" } });
    expect(screen.getByText("Sync failed")).toBeInTheDocument();
    emit({ type: "vars.synced", ref: "", payload: { server_id: "srv-b", revision: 1 } });
    expect(screen.getByText("Sync failed")).toBeInTheDocument();
    emit({ type: "vars.synced", ref: "", payload: { server_id: "srv-a", revision: 3 } });
    expect(screen.getByText("Synced")).toBeInTheDocument();
  });

  it("asks for missing org secrets and encrypts them for this server only", async () => {
    const user = userEvent.setup();
    const api = fakeApi();
    renderCard(api);
    expect(await screen.findByText("1 org secret isn't available on this server")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Provide values" }));
    expect(screen.getByText("Encrypts the value for: alpha")).toBeInTheDocument();
    await user.type(screen.getByLabelText("Value"), "s3cret");
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(api.saveOrg).toHaveBeenCalledWith({
      name: "SMTP_PASS", is_secret: true, description: "", ciphertexts: { "srv-a": "ct(KA):s3cret" },
    });
    expect(api.listServer).toHaveBeenCalledTimes(2); // reloaded after the save
  });

  it("resyncs, reloads stale apps and reloads by variable", async () => {
    const user = userEvent.setup();
    const api = fakeApi();
    renderCard(api);
    await screen.findByText("local");
    await user.click(screen.getByRole("button", { name: "Reload stale apps (2)" }));
    expect(api.reload).toHaveBeenCalledWith({ server_id: "srv-a" });
    await user.click(screen.getByRole("button", { name: "Resync" }));
    expect(api.resync).toHaveBeenCalledWith({ server_id: "srv-a" });
    await user.click(screen.getByRole("button", { name: "Reload apps using SMTP_HOST" }));
    expect(api.reload).toHaveBeenCalledWith({ server_id: "srv-a", names: ["SMTP_HOST"] });
  });

  it("toasts a failed app reload for this server only", async () => {
    const { emit } = renderCard(fakeApi());
    await screen.findByText("local");
    emit({ type: "vars.reloaded", ref: "", payload: { server_id: "srv-b", app_id: "x", ok: false, error: "other", names: [] } });
    emit({ type: "vars.reloaded", ref: "", payload: { server_id: "srv-a", app_id: "y", ok: false, error: "compose failed", names: [] } });
    expect(toast.error).toHaveBeenCalledTimes(1);
    expect(toast.error).toHaveBeenCalledWith("Couldn't reload an app", { id: "vars-reload-failed", description: "compose failed" });
  });

  it("adds a server variable", async () => {
    const user = userEvent.setup();
    const api = fakeApi();
    renderCard(api);
    await screen.findByText("local");
    await user.click(screen.getByRole("button", { name: "Add variable" }));
    await user.type(screen.getByLabelText("Name"), "LOCAL_DIR");
    await user.type(screen.getByLabelText("Value"), "/srv");
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(api.saveServer).toHaveBeenCalledWith("srv-a", { name: "LOCAL_DIR", is_secret: false, value: "/srv", description: "" });
  });

  it("deletes after confirmation", async () => {
    const user = userEvent.setup();
    const api = fakeApi();
    renderCard(api);
    await screen.findByText("local");
    await user.click(screen.getByRole("button", { name: "Delete SMTP_HOST" }));
    await user.click(screen.getByRole("button", { name: "Delete" }));
    expect(api.deleteServer).toHaveBeenCalledWith("srv-a", "s1");
  });

  it("never shows the previous server's variables after switching servers", async () => {
    const api = fakeApi();
    const { switchTo } = renderCard(api);
    expect(await screen.findByText("local")).toBeInTheDocument();
    api.listServer.mockReturnValue(new Promise(() => {})); // the new server's fetch never lands
    switchTo("srv-b");
    expect(screen.queryByText("local")).not.toBeInTheDocument();
    expect(screen.queryByText("Synced")).not.toBeInTheDocument();
    expect(screen.queryByText("Sync pending")).not.toBeInTheDocument();
  });

  it("ignores a late response for a server that was switched away from", async () => {
    const api = fakeApi();
    let lateA: (v: ServerVariables) => void = () => {};
    api.listServer.mockImplementation((id: string) =>
      id === "srv-a"
        ? new Promise<ServerVariables>((r) => (lateA = r))
        : Promise.resolve({ ...SERVER, variables: [{ ...SERVER.variables[0], value: "from-b" }] }),
    );
    const { switchTo } = renderCard(api);
    switchTo("srv-b");
    expect(await screen.findByText("from-b")).toBeInTheDocument();
    await act(async () => lateA(SERVER));
    expect(screen.getByText("from-b")).toBeInTheDocument();
    expect(screen.queryByText("local")).not.toBeInTheDocument();
  });

  it("reverts the pending badge when a resync fails", async () => {
    const user = userEvent.setup();
    const api = fakeApi();
    api.listServer.mockResolvedValue({ ...SERVER, sync: { ...SERVER.sync, state: "synced" } });
    api.resync.mockRejectedValue(new Error("nope"));
    renderCard(api);
    await screen.findByText("Synced");
    await user.click(screen.getByRole("button", { name: "Resync" }));
    expect(await screen.findByText("Synced")).toBeInTheDocument();
  });

  it("shows the org's reload rollout, and a failure naming the app", async () => {
    const { emit } = renderCard(fakeApi(), "srv-a", [
      rollout({ kind: "vars_reload", done: 0, skipped: 0, total: 2, current_app_name: "shop", current_server_id: "srv-b" }),
    ]);
    expect(await screen.findByText("Reloading 1 of 2: shop on beta…")).toBeInTheDocument();
    emit({
      type: "rollout",
      ref: "",
      payload: rollout({ kind: "vars_reload", state: "failed", current_app_name: "shop", current_server_id: "srv-b", error: "compose up failed" }),
    });
    expect(screen.getByText(/Reload stopped at shop on beta/)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Open shop" })).toHaveAttribute("href", "/server/srv-b/app/a1b2c3d4e5f6");
  });
});
