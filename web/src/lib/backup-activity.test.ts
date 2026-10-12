import { act, renderHook } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import {
  holdHealth,
  isRunning,
  OVERLAY_HOLD_MS,
  overlayFor,
  releaseHealth,
  resetHeldHealth,
  restoringBackup,
  shownHealth,
  trackActivity,
  useActivity,
  useOverlays,
} from "@/lib/backup-activity";

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

describe("held health", () => {
  const at = 1_800_000_000_000;
  const secs = at / 1000;

  it("holds a finished copy until a report shows a newer one", () => {
    const prior = { name: "db", state: "ok" as const, last_success: secs - 3600 };
    const o = overlayFor("db", { kind: "backed-up", unchanged: false }, prior, at);
    expect(o.health).toEqual({ name: "db", state: "ok", last_success: secs });
    expect(shownHealth(o, prior, at + 1000)).toEqual(o.health);
    expect(shownHealth(o, { name: "db", state: "running" }, at + 1000)).toEqual(o.health);
    expect(shownHealth(o, undefined, at + 1000)).toEqual(o.health);
    const fresh = { name: "db", state: "ok" as const, last_success: secs + 2 };
    expect(shownHealth(o, fresh, at + 1000)).toBe(fresh);
    expect(o.until).toBe(at + OVERLAY_HOLD_MS);
    expect(shownHealth(o, prior, at + OVERLAY_HOLD_MS)).toBe(prior);
    expect(shownHealth(undefined, prior, at)).toBe(prior);
  });

  it("takes an unchanged run's report at the same last success", () => {
    const prior = { name: "db", state: "ok" as const, last_success: secs - 60 };
    const o = overlayFor("db", { kind: "backed-up", unchanged: true }, prior, at);
    expect(shownHealth(o, prior, at + 1)).toBe(prior);
    expect(shownHealth(o, { name: "db", state: "failing" }, at + 1)).toEqual(o.health);
  });

  it("holds a failure, an off and an on until a report shows them", () => {
    const prior = { name: "db", state: "ok" as const, last_success: secs - 60 };
    const failed = overlayFor("db", { kind: "failed", error: "boom" }, prior, at);
    expect(failed.health).toEqual({ name: "db", state: "failing", last_success: secs - 60, last_error: "boom" });
    expect(shownHealth(failed, prior, at + 1)).toEqual(failed.health);
    const failing = { name: "db", state: "failing" as const, last_error: "boom" };
    expect(shownHealth(failed, failing, at + 1)).toBe(failing);
    const later = { name: "db", state: "ok" as const, last_success: secs + 30 };
    expect(shownHealth(failed, later, at + 1)).toBe(later);

    const off = overlayFor("db", { kind: "off" }, failing, at);
    expect(shownHealth(off, failing, at + 1)).toEqual({ name: "db", state: "off" });
    expect(shownHealth(off, undefined, at + 1)).toBeUndefined();
    const on = overlayFor("db", { kind: "on" }, undefined, at);
    expect(shownHealth(on, { name: "db", state: "off" }, at + 1)).toEqual({ name: "db", state: "never" });
    expect(shownHealth(on, undefined, at + 1)).toEqual({ name: "db", state: "never" });
    const never = { name: "db", state: "never" as const };
    expect(shownHealth(on, never, at + 1)).toBe(never);
  });

  it("is kept across mounts, released once and forgotten", () => {
    resetHeldHealth();
    const { result } = renderHook(() => useOverlays("a9"));
    expect(result.current).toEqual({});
    act(() => holdHealth("a9", "db", { kind: "off" }, undefined, Date.now()));
    expect(Object.keys(result.current)).toEqual(["db"]);
    // Expired entries are dropped when the next one is held.
    act(() => holdHealth("a9", "old", { kind: "off" }, undefined, Date.now() - 2 * OVERLAY_HOLD_MS));
    act(() => holdHealth("a9", "other", { kind: "on" }, undefined, Date.now()));
    expect(Object.keys(result.current).sort()).toEqual(["db", "other"]);
    act(() => releaseHealth("a9", "db"));
    act(() => releaseHealth("a9", "db")); // twice: nothing more
    expect(Object.keys(result.current)).toEqual(["other"]);
    act(() => resetHeldHealth());
    expect(result.current).toEqual({});
  });
});
