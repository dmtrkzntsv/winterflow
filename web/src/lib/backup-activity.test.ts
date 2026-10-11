import { act, renderHook } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { isRunning, restoringBackup, trackActivity, useActivity } from "@/lib/backup-activity";

describe("backup activity", () => {
  it("follows what runs for an app, across mounts", () => {
    const { result, unmount } = renderHook(() => useActivity("a1"));
    expect(result.current).toEqual({});
    let end = () => {};
    act(() => {
      end = trackActivity("a1", "database", "backup");
    });
    expect(result.current).toEqual({ database: ["backup"] });
    unmount();

    // A remounted tab still sees the run in flight; another app doesn't.
    const again = renderHook(() => useActivity("a1"));
    const other = renderHook(() => useActivity("a2"));
    expect(again.result.current).toEqual({ database: ["backup"] });
    expect(other.result.current).toEqual({});
    let endRestore = () => {};
    act(() => {
      endRestore = trackActivity("a1", "media", "restore");
    });
    expect(isRunning(again.result.current, "database", "backup")).toBe(true);
    expect(isRunning(again.result.current, "database", "restore")).toBe(false);
    expect(restoringBackup(again.result.current)).toBe("media");
    act(() => {
      end();
      end(); // ending twice clears nothing more
      endRestore();
    });
    expect(again.result.current).toEqual({});
    expect(restoringBackup(again.result.current)).toBeUndefined();
  });

  it("ends only its own run when a backup's runs overlap", () => {
    const { result } = renderHook(() => useActivity("a3"));
    let endBackup = () => {};
    let endRestore = () => {};
    act(() => {
      endBackup = trackActivity("a3", "database", "backup");
      endRestore = trackActivity("a3", "database", "restore");
    });
    act(() => endBackup());
    expect(result.current).toEqual({ database: ["restore"] });
    expect(restoringBackup(result.current)).toBe("database");
    act(() => endRestore());
    expect(result.current).toEqual({});
  });
});
