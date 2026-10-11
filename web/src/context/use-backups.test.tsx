import { renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { BACKUP_RUN_TIMEOUT_MS, useBackups } from "@/context/use-backups";

vi.mock("@/context/use-servers", () => ({ useServers: () => ({ activeServerId: "srv-1" }) }));
const waitFor = vi.fn();
vi.mock("@/context/use-notifications", () => ({ useNotifications: () => ({ waitFor }) }));

type Sent = { url: string; method: string; body: unknown };
let sent: Sent[] = [];
const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
  const url = String(input);
  sent.push({ url, method: init?.method ?? "GET", body: init?.body ? JSON.parse(String(init.body)) : undefined });
  if (url.includes("/api/v1/storage/get-storages")) {
    return new Response(JSON.stringify({ data: { storages: [{ name: "disk" }, { name: "s3" }] } }), { status: 200 });
  }
  return new Response(JSON.stringify({ data: { request_id: "r1" } }), { status: 202 });
});

describe("useBackups", () => {
  beforeEach(() => {
    sent = [];
    waitFor.mockReset();
    vi.stubGlobal("fetch", fetchMock);
  });
  afterEach(() => vi.unstubAllGlobals());

  it("sends server_id in the body of the POST routes", async () => {
    const { result } = renderHook(() => useBackups());
    waitFor.mockResolvedValueOnce({ status: 0, payload: { backups: [{ name: "db" }], redeployed: true } });
    expect(await result.current.updateBackups("a1", [{ name: "db", enabled: true, storage: "disk" }])).toEqual({
      backups: [{ name: "db" }],
      redeployed: true,
      warnings: [],
    });
    expect(sent[0]).toEqual({
      url: expect.stringMatching(/\/api\/v1\/app\/update-backups$/),
      method: "POST",
      body: { server_id: "srv-1", app_id: "a1", backups: [{ name: "db", enabled: true, storage: "disk" }] },
    });

    waitFor.mockResolvedValueOnce({ status: 0, payload: { unchanged: true } });
    expect(await result.current.runBackup("a1", "db")).toEqual({ unchanged: true, point: null, warnings: [] });
    expect(waitFor).toHaveBeenLastCalledWith("r1", BACKUP_RUN_TIMEOUT_MS);

    waitFor.mockResolvedValueOnce({ status: 0, payload: { restored_point: "p1", pre_restore_point: "p2", warnings: ["w"] } });
    expect(await result.current.restoreBackup("a1", "db", "p1")).toEqual({ restored_point: "p1", pre_restore_point: "p2", warnings: ["w"] });
    expect(sent[2].body).toEqual({ server_id: "srv-1", app_id: "a1", backup: "db", point: "p1" });

    waitFor.mockResolvedValueOnce({ status: 0, payload: {} });
    await result.current.deleteRestorePoint("a1", "db", "p1");
    waitFor.mockResolvedValueOnce({ status: 0, payload: { deleted: 6 } });
    expect(await result.current.deleteOrphan("disk", "gone")).toBe(6);
    expect(sent[4].body).toEqual({ server_id: "srv-1", name: "disk", app_id: "gone" });
  });

  it("puts server_id in the query of the GET routes", async () => {
    const { result } = renderHook(() => useBackups());
    waitFor.mockResolvedValueOnce({ status: 0, payload: { points: null } });
    expect(await result.current.listRestorePoints("a1", "db")).toEqual([]);
    expect(sent[0].url).toContain("/api/v1/app/list-restore-points?server_id=srv-1&app_id=a1&backup=db");
    waitFor.mockResolvedValueOnce({ status: 0, payload: { orphans: [{ app_id: "gone" }] } });
    expect(await result.current.listOrphans("disk")).toEqual([{ app_id: "gone" }]);
    expect(sent[1].url).toContain("/api/v1/storage/list-orphans?server_id=srv-1&name=disk");
    expect(await result.current.serverStorages()).toEqual(["disk", "s3"]);
  });

  it("surfaces refusals and failed results", async () => {
    const { result } = renderHook(() => useBackups());
    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify({ message: "invalid restore point id" }), { status: 400 }));
    await expect(result.current.restoreBackup("a1", "db", "x")).rejects.toThrow("invalid restore point id");
    waitFor.mockResolvedValueOnce({ status: 1, error: "a backup of db is already queued or running" });
    await expect(result.current.runBackup("a1", "db")).rejects.toThrow("a backup of db is already queued or running");
    waitFor.mockResolvedValueOnce({ status: 1 });
    await expect(result.current.listOrphans("disk")).rejects.toThrow("Operation failed");
    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify({ data: {} }), { status: 202 }));
    await expect(result.current.listOrphans("disk")).rejects.toThrow("No request id");
    fetchMock.mockResolvedValueOnce(new Response("down", { status: 502 }));
    await expect(result.current.serverStorages()).rejects.toThrow("Request failed: 502");
  });
});
