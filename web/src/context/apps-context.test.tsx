import type { ReactNode } from "react";
import { act, renderHook, waitFor as waitUntil } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { AppsProvider } from "@/context/apps-context";
import { DELETE_APP_TIMEOUT_MS, SaveFailedError } from "@/context/apps-context-base";
import { useApps } from "@/context/use-apps";
import { keepRevisions, keptRevisions, resetRevisionsCache, revisionsEpoch } from "@/lib/revisions";

vi.mock("@/context/use-auth", () => ({ useAuth: () => ({ isAuthenticated: true }) }));
vi.mock("@/context/use-servers", () => ({ useServers: () => ({ activeServerId: "srv-1" }) }));
const waitFor = vi.fn();
const handlers: ((n: { type: string; ref?: string; payload?: unknown }) => void)[] = [];
vi.mock("@/context/use-notifications", () => ({
  useNotifications: () => ({
    subscribe: (h: (typeof handlers)[number]) => {
      handlers.push(h);
      return () => {};
    },
    waitFor,
    connected: true,
  }),
}));

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status });

// The API refuses a save whose compose file references a storage the server
// doesn't have with a 400 and the reason.
const refusal = "unresolved storage variable: ${WF_STORAGE_NOPE_PATH}: there is no storage \"nope\" on this server";

const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
  const url = String(input);
  if (url.includes("/api/v1/app/get-apps?")) return json(200, { data: { apps: [] } });
  if (url.includes("/api/v1/app/get-apps-status")) {
    return json(200, {
      data: {
        apps: [
          { app_id: "a1", status_code: 1, backups: [{ name: "db", state: "late", last_success: 1 }], containers: [{ container_id: "c1", name: "shop-web-1", status_code: 1, exit_code: 0 }] },
          { app_id: "a2", status_code: 5, backups: null },
        ],
      },
    });
  }
  if (url.includes("/api/v1/app/delete-app")) return json(202, { data: { request_id: "del-1" } });
  if (url.includes("/api/v1/app/refresh-apps")) return json(202, { data: {} });
  if (url.includes("/api/v1/app/save-app")) return json(400, { success: false, message: refusal });
  if (url.includes("/api/v1/app/control-app")) return new Response("bad gateway", { status: 502 });
  return json(500, {});
});

const wrapper = ({ children }: { children: ReactNode }) => <AppsProvider>{children}</AppsProvider>;

describe("AppsProvider commands", () => {
  beforeEach(() => vi.stubGlobal("fetch", fetchMock));
  afterEach(() => vi.unstubAllGlobals());

  it("rejects a refused save with the server's reason, which the editor shows", async () => {
    const { result } = renderHook(() => useApps(), { wrapper });
    await waitUntil(() => expect(result.current.loading).toBe(false));
    await expect(
      result.current.saveApp({ app: { id: "a1" }, config: {}, files: [], variables: [] }),
    ).rejects.toThrow(refusal);
    expect(waitFor).not.toHaveBeenCalled();
  });

  it("falls back to the status when the refusal has no message", async () => {
    const { result } = renderHook(() => useApps(), { wrapper });
    await waitUntil(() => expect(result.current.loading).toBe(false));
    await expect(result.current.control("a1", "start")).rejects.toThrow("Request failed: 502");
  });
});

describe("AppsProvider backups", () => {
  beforeEach(() => vi.stubGlobal("fetch", fetchMock));
  afterEach(() => vi.unstubAllGlobals());

  it("keeps each app's containers from the status and its pushes", async () => {
    const { result } = renderHook(() => useApps(), { wrapper });
    await waitUntil(() =>
      expect(result.current.containersByApp).toEqual({
        a1: [{ container_id: "c1", name: "shop-web-1", status_code: 1, exit_code: 0 }],
      }),
    );
    const crashed = { container_id: "c2", name: "blog-db-1", status_code: 4, exit_code: 1, error: "out of memory" };
    act(() => {
      for (const h of handlers) {
        h({ type: "apps_status", payload: { server_id: "srv-1", apps: [{ app_id: "a2", status_code: 4, containers: [crashed] }] } });
      }
    });
    await waitUntil(() => expect(result.current.containersByApp).toEqual({ a2: [crashed] }));
  });

  it("keeps each app's backup health from the status and its pushes", async () => {
    const { result } = renderHook(() => useApps(), { wrapper });
    await waitUntil(() => expect(result.current.backupsByApp.a1).toEqual([{ name: "db", state: "late", last_success: 1 }]));
    expect(result.current.statusByApp).toEqual({ a1: 1, a2: 5 });
    expect(result.current.backupsByApp.a2).toBeUndefined();

    act(() => {
      for (const h of handlers) {
        h({ type: "apps_status", payload: { server_id: "srv-1", apps: [{ app_id: "a2", status_code: 1, backups: [{ name: "media", state: "failing" }] }] } });
        h({ type: "apps_status", payload: { server_id: "other", apps: [] } });
      }
    });
    await waitUntil(() => expect(result.current.backupsByApp).toEqual({ a2: [{ name: "media", state: "failing" }] }));
  });

  it("asks to delete the backups with the app and reports whether they were", async () => {
    const { result } = renderHook(() => useApps(), { wrapper });
    await waitUntil(() => expect(result.current.loading).toBe(false));
    waitFor.mockResolvedValueOnce({ status: 0, payload: { warnings: [], volumes_deleted: false, backups_deleted: true } });
    await expect(result.current.remove("a1", { deleteBackups: true })).resolves.toEqual({
      warnings: [],
      volumesDeleted: false,
      backupsDeleted: true,
    });
    // fetch's second argument, which the mock's signature leaves out.
    const del = fetchMock.mock.calls.find(([u]) => String(u).includes("/api/v1/app/delete-app")) as unknown as [string, RequestInit];
    expect(JSON.parse(String(del[1].body))).toEqual({
      app_id: "a1",
      delete_volumes: false,
      delete_backups: true,
      server_id: "srv-1",
    });
  });

  it("waits for a delete longer than the agent can take", async () => {
    const { result } = renderHook(() => useApps(), { wrapper });
    await waitUntil(() => expect(result.current.loading).toBe(false));
    waitFor.mockClear();
    waitFor.mockResolvedValueOnce({ status: 0, payload: {} });
    await result.current.remove("a1");
    // The agent's worst case is about 8 minutes (backups settled in every storage).
    expect(waitFor).toHaveBeenLastCalledWith("del-1", DELETE_APP_TIMEOUT_MS);
    expect(DELETE_APP_TIMEOUT_MS).toBeGreaterThanOrEqual(9 * 60_000);
  });
});

describe("AppsProvider apps", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("knows which apps a template manages", async () => {
    const app = { server_id: "srv-1", name: "shop", version: "1", icon: "", color: "", created_at: "" };
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) =>
        String(input).includes("/api/v1/app/get-apps?")
          ? json(200, { data: { apps: [{ ...app, id: "a1", template_id: "t1" }, { ...app, id: "a2", template_id: "" }] } })
          : json(202, { data: {} }),
      ),
    );
    const { result } = renderHook(() => useApps(), { wrapper });
    await waitUntil(() => expect(result.current.apps).toHaveLength(2));
    expect(result.current.apps.map((a) => a.templateId)).toEqual(["t1", undefined]);
  });
});

describe("AppsProvider and the kept histories", () => {
  const history = { current: "c", deployed: "b", revisions: [] };
  const accepting = vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    if (/control-app|rollback-app|delete-app/.test(url)) return json(202, { data: { request_id: "r1" } });
    return fetchMock(input);
  });
  beforeEach(() => {
    vi.stubGlobal("fetch", accepting);
    waitFor.mockReset().mockResolvedValue({ status: 0, payload: {} });
    resetRevisionsCache();
    keepRevisions("a1", "3", history, 0);
  });
  afterEach(() => vi.unstubAllGlobals());

  it("forgets an app's history when a control or a roll back succeeds, so a draft that went live is read again", async () => {
    const { result } = renderHook(() => useApps(), { wrapper });
    await waitUntil(() => expect(result.current.loading).toBe(false));
    await act(() => result.current.control("a1", "start"));
    expect(keptRevisions("a1", "3")).toBeNull();
    expect(revisionsEpoch("a1")).toBe(1);
    keepRevisions("a1", "3", history, 1);
    await act(() => result.current.rollback("a1", "abc"));
    expect(keptRevisions("a1", "3")).toBeNull();
    expect(revisionsEpoch("a1")).toBe(2);
  });

  it("keeps it across a stop or a restart, which move nothing; a pull forgets it", async () => {
    const { result } = renderHook(() => useApps(), { wrapper });
    await waitUntil(() => expect(result.current.loading).toBe(false));
    await act(() => result.current.control("a1", "stop"));
    await act(() => result.current.control("a1", "restart"));
    expect(keptRevisions("a1", "3")).toEqual(history);
    expect(revisionsEpoch("a1")).toBe(0);
    await act(() => result.current.control("a1", "update"));
    expect(keptRevisions("a1", "3")).toBeNull();
    expect(revisionsEpoch("a1")).toBe(1);
  });

  it("keeps it when the control fails", async () => {
    vi.stubGlobal("fetch", fetchMock);
    const { result } = renderHook(() => useApps(), { wrapper });
    await waitUntil(() => expect(result.current.loading).toBe(false));
    await expect(result.current.control("a1", "start")).rejects.toThrow();
    expect(keptRevisions("a1", "3")).toEqual(history);
    expect(revisionsEpoch("a1")).toBe(0);
  });

  it("drops an app's bookkeeping with the app", async () => {
    const { result } = renderHook(() => useApps(), { wrapper });
    await waitUntil(() => expect(result.current.loading).toBe(false));
    await act(() => result.current.control("a1", "stop"));
    waitFor.mockResolvedValueOnce({ status: 0, payload: {} });
    await act(() => result.current.remove("a1"));
    expect(keptRevisions("a1", "3")).toBeNull();
    expect(revisionsEpoch("a1")).toBe(0);
  });
});

describe("AppsProvider failed deploys", () => {
  // save-app is accepted (its result comes over SSE), refresh-apps too.
  const accepting = vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    if (url.includes("/api/v1/app/save-app")) return json(202, { data: { request_id: "save-1" } });
    if (url.includes("/api/v1/app/refresh-apps")) return json(202, { data: { request_id: "list-1" } });
    if (url.includes("/api/v1/app/control-app")) return json(202, { data: { request_id: "ctl-1" } });
    return fetchMock(input);
  });
  beforeEach(() => {
    waitFor.mockReset();
    vi.stubGlobal("fetch", accepting);
  });
  afterEach(() => vi.unstubAllGlobals());

  it("rejects a save the agent failed with a SaveFailedError", async () => {
    const { result } = renderHook(() => useApps(), { wrapper });
    await waitUntil(() => expect(result.current.loading).toBe(false));
    waitFor.mockResolvedValueOnce({ status: 1, error: "docker compose up: exit status 1: port is already allocated" });
    const err = await result.current.saveApp({ app: { name: "web" }, config: {}, files: [], variables: [] }).catch((e: unknown) => e);
    expect(err).toBeInstanceOf(SaveFailedError);
    expect((err as Error).message).toBe("docker compose up: exit status 1: port is already allocated");
  });

  it("reconciles: waits for the agent's list, then reads the catalog again", async () => {
    const { result } = renderHook(() => useApps(), { wrapper });
    await waitUntil(() => expect(result.current.loading).toBe(false));
    waitFor.mockResolvedValueOnce({ status: 0 });
    await expect(result.current.reconcile()).resolves.toEqual([]);
    expect(waitFor).toHaveBeenCalledWith("list-1", undefined);
  });

  it("holds a noted failure until a report shows the app running, or a start goes through", async () => {
    const { result } = renderHook(() => useApps(), { wrapper });
    await waitUntil(() => expect(result.current.statusByApp).toEqual({ a1: 1, a2: 5 }));
    act(() => {
      result.current.noteDeployFailure("a2", "boom");
      result.current.noteDeployFailure("a3", "bang");
    });
    expect(result.current.deployFailures).toEqual({ a2: "boom", a3: "bang" });
    // A report that still shows it stopped keeps it.
    act(() => {
      for (const h of handlers) h({ type: "apps_status", payload: { server_id: "srv-1", apps: [{ app_id: "a2", status_code: 2 }] } });
    });
    expect(result.current.deployFailures).toEqual({ a2: "boom", a3: "bang" });
    // A report showing it running ends it.
    act(() => {
      for (const h of handlers) h({ type: "apps_status", payload: { server_id: "srv-1", apps: [{ app_id: "a2", status_code: 1 }] } });
    });
    await waitUntil(() => expect(result.current.deployFailures).toEqual({ a3: "bang" }));
    // A stop leaves it; a start that went through ends it.
    waitFor.mockResolvedValue({ status: 0 });
    await act(() => result.current.control("a3", "stop"));
    expect(result.current.deployFailures).toEqual({ a3: "bang" });
    await act(() => result.current.control("a3", "start"));
    expect(result.current.deployFailures).toEqual({});
  });

  it("ends a noted failure when a save of the app goes through", async () => {
    const { result } = renderHook(() => useApps(), { wrapper });
    await waitUntil(() => expect(result.current.loading).toBe(false));
    act(() => result.current.noteDeployFailure("a9", "boom"));
    waitFor.mockResolvedValue({ status: 0 });
    await act(async () => {
      await result.current.saveApp({ draft: true, app: { id: "a9" }, config: {}, files: [], variables: [] });
    });
    // A draft deploys nothing.
    expect(result.current.deployFailures).toEqual({ a9: "boom" });
    await act(async () => {
      await result.current.saveApp({ app: { id: "a9" }, config: {}, files: [], variables: [] });
    });
    expect(result.current.deployFailures).toEqual({});
  });
});
