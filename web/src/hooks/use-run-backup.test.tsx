import type { ReactNode } from "react";
import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { AppsContext, type AppsContextValue } from "@/context/apps-context-base";
import { useBackUpNow, useRunBackup } from "@/hooks/use-run-backup";
import { resetHeldHealth, trackActivity } from "@/lib/backup-activity";
import { AgentError, type EffectiveBackup } from "@/lib/backups";

const toast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn(), warning: vi.fn(), info: vi.fn() }));
vi.mock("sonner", () => ({ toast }));
const backups = vi.hoisted(() => ({ runBackup: vi.fn() }));
vi.mock("@/context/use-backups", () => ({ useBackups: () => backups }));

const b = (name: string, over: Partial<EffectiveBackup> = {}): EffectiveBackup => ({
  name,
  type: "sqlite",
  enabled: true,
  storage: "disk",
  in_recipe: true,
  from_recipe: null,
  ...over,
});

function wrap(getApp: AppsContextValue["getApp"]) {
  const value = { getApp, backupsByApp: { a1: [{ name: "database", state: "ok", last_success: 10 }] } } as unknown as AppsContextValue;
  return ({ children }: { children: ReactNode }) => <AppsContext.Provider value={value}>{children}</AppsContext.Provider>;
}

describe("useRunBackup", () => {
  beforeEach(() => {
    resetHeldHealth();
    backups.runBackup.mockReset();
    for (const f of Object.values(toast)) f.mockReset();
  });

  it("backs one backup up and says how it went", async () => {
    backups.runBackup.mockResolvedValueOnce({ unchanged: true, point: null, warnings: [] });
    const { result } = renderHook(() => useRunBackup("a1"), { wrapper: wrap(vi.fn()) });
    await act(() => result.current("database"));
    expect(backups.runBackup).toHaveBeenCalledWith("a1", "database");
    expect(toast.success).toHaveBeenCalledWith("database: nothing changed since the last copy", { description: undefined });
    backups.runBackup.mockRejectedValueOnce(new AgentError("upload: permission denied"));
    await act(() => result.current("database"));
    expect(toast.error).toHaveBeenCalledWith("Couldn't back up database", { description: "upload: permission denied" });
  });
});

describe("useBackUpNow", () => {
  beforeEach(() => {
    resetHeldHealth();
    backups.runBackup.mockReset();
    for (const f of Object.values(toast)) f.mockReset();
  });

  it("runs every backup it can: on, with a storage, not custom", async () => {
    backups.runBackup.mockResolvedValue({ unchanged: false, point: { id: "p" }, warnings: [] });
    const getApp = vi.fn(async () => ({
      backups: [
        b("database"),
        b("media", { enabled: false }),
        b("nowhere", { storage: "" }),
        b("replica", { type: "custom" }),
        b("uploads", { type: "files" }),
      ],
    }));
    const { result } = renderHook(() => useBackUpNow("a1"), { wrapper: wrap(getApp as unknown as AppsContextValue["getApp"]) });
    await act(() => result.current.run());
    expect(backups.runBackup.mock.calls).toEqual([
      ["a1", "database"],
      ["a1", "uploads"],
    ]);
  });

  it("sends nothing and says so when no backup is set up", async () => {
    const getApp = vi.fn(async () => ({ backups: [b("media", { enabled: false }), b("replica", { type: "custom" })] }));
    const { result } = renderHook(() => useBackUpNow("a1"), { wrapper: wrap(getApp as unknown as AppsContextValue["getApp"]) });
    await act(() => result.current.run());
    expect(backups.runBackup).not.toHaveBeenCalled();
    expect(toast.info).toHaveBeenCalledWith("Nothing to back up yet", { description: "Set up a backup on the Backups tab first." });
  });

  it("reports an app it can't read", async () => {
    const getApp = vi.fn(async () => Promise.reject(new Error("agent offline")));
    const { result } = renderHook(() => useBackUpNow("a1"), { wrapper: wrap(getApp as unknown as AppsContextValue["getApp"]) });
    await act(() => result.current.run());
    expect(toast.error).toHaveBeenCalledWith("Couldn't read the app's backups", { description: "agent offline" });
  });

  it("is busy while any backup of the app runs or is restored", async () => {
    const { result } = renderHook(() => useBackUpNow("a1"), { wrapper: wrap(vi.fn()) });
    expect(result.current.busy).toBe(false);
    let end = () => {};
    act(() => {
      end = trackActivity("a1", "database", "restore");
    });
    expect(result.current.busy).toBe(true);
    expect(result.current.restoring).toBe(true);
    act(() => end());
    await waitFor(() => expect(result.current.busy).toBe(false));
    expect(result.current.restoring).toBe(false);
    act(() => {
      end = trackActivity("a1", "database", "backup");
    });
    expect(result.current.busy).toBe(true);
    expect(result.current.restoring).toBe(false);
    act(() => end());
  });
});
