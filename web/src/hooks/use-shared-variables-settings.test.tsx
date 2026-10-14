import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { toast } from "sonner";
import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

import { useSharedVariablesSettings } from "@/hooks/use-shared-variables-settings";
import type {
  EncryptionKeys,
  OrgVariables,
  ServerVariables,
  SharedVariable,
  SharedVariablesApi,
} from "@/lib/shared-variables";
import { fakeNotifications } from "@/test/notifications";

const T = "2026-10-11T00:00:00Z";
const plain = (over: Partial<SharedVariable>): SharedVariable => ({
  id: "x", name: "X", scope: "org", is_secret: false, value: "v", description: "",
  updated_at: T, updated_by: "u", missing_on: [], ...over,
});
const ORG: OrgVariables = {
  variables: [plain({ id: "o1", name: "SMTP_HOST", value: "mail" })],
  servers: [
    { server_id: "srv-a", name: "alpha", has_key: true, revision: 3, agent_revision: 3, state: "synced" },
    { server_id: "srv-b", name: "beta", has_key: true, revision: 3, agent_revision: 1, state: "pending" },
  ],
};
const server = (id: string, value: string): ServerVariables => ({
  variables: [plain({ id: `s-${id}`, name: "LOCAL_DIR", scope: "server", value })],
  inherited: [],
  builtins: [{ name: "WF_SERVER_NAME", value: id, description: "This server's name" }],
  missing_org_secrets: [],
  sync: { server_id: id, name: id, has_key: true, revision: 3, agent_revision: 2, state: "pending" },
});
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
    listServer: vi.fn((id: string) => Promise.resolve(server(id, `dir-${id}`))),
    keys: vi.fn().mockResolvedValue(KEYS),
    saveOrg: vi.fn().mockResolvedValue({}),
    saveServer: vi.fn().mockResolvedValue({}),
    deleteOrg: vi.fn().mockResolvedValue(undefined),
    deleteServer: vi.fn().mockResolvedValue(undefined),
    resync: vi.fn().mockResolvedValue([]),
    reload: vi.fn().mockResolvedValue([]),
  };
}

function setup(api: ReturnType<typeof fakeApi>, serverId: string | null) {
  const sse = fakeNotifications();
  const wrapper = ({ children }: { children: ReactNode }) => <sse.Provider>{children}</sse.Provider>;
  const view = renderHook(
    ({ id }: { id: string | null }) => useSharedVariablesSettings(api as unknown as SharedVariablesApi, id),
    { wrapper, initialProps: { id: serverId } },
  );
  return { ...view, emit: (n: Parameters<typeof sse.emit>[0]) => act(() => sse.emit(n)) };
}

describe("useSharedVariablesSettings", () => {
  beforeEach(() => vi.clearAllMocks());

  it("loads the org's variables, this server's and the keys", async () => {
    const api = fakeApi();
    const { result } = setup(api, "srv-a");
    await waitFor(() => expect(result.current.server?.variables[0].value).toBe("dir-srv-a"));
    expect(result.current.org?.variables[0].name).toBe("SMTP_HOST");
    expect(api.listServer).toHaveBeenCalledWith("srv-a");
    // Each server's sync state: the org list's, then this server's own report.
    expect(result.current.sync).toEqual({ "srv-a": "pending", "srv-b": "pending" });
    expect(result.current.serverNames.get("srv-b")).toBe("beta");
  });

  it("loads only the org's without a server", async () => {
    const api = fakeApi();
    const { result } = setup(api, null);
    await waitFor(() => expect(result.current.org).not.toBeNull());
    expect(result.current.server).toBeNull();
    expect(api.listServer).not.toHaveBeenCalled();
  });

  it("never shows the previous server's rows after a switch", async () => {
    const api = fakeApi();
    const { result, rerender } = setup(api, "srv-a");
    await waitFor(() => expect(result.current.server).not.toBeNull());
    api.listServer.mockReturnValue(new Promise(() => {})); // the new server's answer never lands
    rerender({ id: "srv-b" });
    expect(result.current.server).toBeNull();
    expect(result.current.org).toBeNull();
  });

  it("drops a late answer for a server switched away from", async () => {
    const api = fakeApi();
    let lateA: (v: ServerVariables) => void = () => {};
    api.listServer.mockImplementation((id: string) =>
      id === "srv-a" ? new Promise<ServerVariables>((r) => (lateA = r)) : Promise.resolve(server(id, "from-b")),
    );
    const { result, rerender } = setup(api, "srv-a");
    rerender({ id: "srv-b" });
    await waitFor(() => expect(result.current.server?.variables[0].value).toBe("from-b"));
    await act(async () => lateA(server("srv-a", "late")));
    expect(result.current.server?.variables[0].value).toBe("from-b");
  });

  it("follows sync states over SSE and toasts a failed reload once per burst", async () => {
    const api = fakeApi();
    const { result, emit } = setup(api, "srv-a");
    await waitFor(() => expect(result.current.org).not.toBeNull());
    emit({ type: "vars.sync_failed", ref: "", payload: { server_id: "srv-b", error: "boom" } });
    expect(result.current.sync["srv-b"]).toBe("failed");
    emit({ type: "vars.synced", ref: "", payload: { server_id: "srv-b", revision: 3 } });
    expect(result.current.sync["srv-b"]).toBe("synced");
    emit({ type: "vars.reloaded", ref: "", payload: { server_id: "srv-b", app_id: "a", ok: false, error: "compose failed", names: [] } });
    expect(toast.error).toHaveBeenCalledWith("Couldn't reload an app", { id: "vars-reload-failed", description: "compose failed" });
  });

  it("saves and deletes in the variable's own scope", async () => {
    const api = fakeApi();
    const { result } = setup(api, "srv-a");
    await waitFor(() => expect(result.current.server).not.toBeNull());
    const body = { name: "A", is_secret: false as const, value: "1", description: "" };
    await act(() => result.current.save(body, "server"));
    expect(api.saveServer).toHaveBeenCalledWith("srv-a", body);
    await act(() => result.current.save(body, "org"));
    expect(api.saveOrg).toHaveBeenCalledWith(body);
    await act(() => result.current.remove(result.current.server!.variables[0]));
    expect(api.deleteServer).toHaveBeenCalledWith("srv-a", "s-srv-a");
    await act(() => result.current.remove(result.current.org!.variables[0]));
    expect(api.deleteOrg).toHaveBeenCalledWith("o1");
  });

  it("reports a refused delete and rethrows it", async () => {
    const api = fakeApi();
    api.deleteOrg.mockRejectedValue(new Error("nope"));
    const { result } = setup(api, null);
    await waitFor(() => expect(result.current.org).not.toBeNull());
    await expect(result.current.remove(result.current.org!.variables[0])).rejects.toThrow("nope");
    expect(toast.error).toHaveBeenCalledWith("Couldn't delete the variable", { description: "nope" });
  });

  it("sends again to failed servers, marking them pending until the answer, back on an error", async () => {
    const api = fakeApi();
    const { result, emit } = setup(api, "srv-a");
    await waitFor(() => expect(result.current.org).not.toBeNull());
    emit({ type: "vars.sync_failed", ref: "", payload: { server_id: "srv-b" } });
    act(() => result.current.sendAgain(["srv-b"]));
    expect(api.resync).toHaveBeenCalledWith({ server_id: "srv-b" });
    expect(result.current.sync["srv-b"]).toBe("pending");
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith("Sent again"));

    api.resync.mockRejectedValueOnce(new Error("offline"));
    emit({ type: "vars.sync_failed", ref: "", payload: { server_id: "srv-a" } });
    act(() => result.current.sendAgain(["srv-a", "srv-b"]));
    expect(api.resync).toHaveBeenLastCalledWith({});
    await waitFor(() => expect(result.current.sync["srv-a"]).toBe("failed"));
  });

  it("reloads like the old pages did", async () => {
    const api = fakeApi();
    const { result } = setup(api, "srv-a");
    await waitFor(() => expect(result.current.server).not.toBeNull());
    act(() => result.current.reloadStale());
    expect(api.reload).toHaveBeenCalledWith({ server_id: "srv-a" });
    act(() => result.current.reloadVariable(result.current.server!.variables[0]));
    expect(api.reload).toHaveBeenCalledWith({ server_id: "srv-a", names: ["LOCAL_DIR"] });
    act(() => result.current.reloadVariable(result.current.org!.variables[0]));
    expect(api.reload).toHaveBeenCalledWith({ variable_id: "o1" });
    act(() => result.current.resendVariable(result.current.org!.variables[0]));
    expect(api.resync).toHaveBeenCalledWith({ variable_id: "o1" });
  });

  it("reads loading, not failed, for a server after another one failed", async () => {
    const api = fakeApi();
    api.listServer.mockRejectedValueOnce(new Error("down"));
    const { result, rerender } = setup(api, "srv-a");
    await waitFor(() => expect(result.current.loadFailed).toBe(true));
    api.listServer.mockReturnValue(new Promise(() => {}));
    rerender({ id: "srv-b" });
    expect(result.current.loadFailed).toBe(false);
  });

  it("says so when the page can't be loaded", async () => {
    const api = fakeApi();
    api.listOrg.mockRejectedValue(new Error("down"));
    const { result } = setup(api, null);
    await waitFor(() => expect(result.current.loadFailed).toBe(true));
    expect(toast.error).toHaveBeenCalledWith("Couldn't load the shared variables", { description: "down" });
  });
});
