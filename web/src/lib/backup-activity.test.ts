import { act, renderHook } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { trackActivity, useActivity } from "@/lib/backup-activity";

describe("backup activity", () => {
  it("follows what runs for an app, across mounts", () => {
    const { result, unmount } = renderHook(() => useActivity("a1"));
    expect(result.current).toEqual({});
    let end = () => {};
    act(() => {
      end = trackActivity("a1", "database", "backup");
    });
    expect(result.current).toEqual({ database: "backup" });
    unmount();

    // A remounted tab still sees the run in flight; another app doesn't.
    const again = renderHook(() => useActivity("a1"));
    const other = renderHook(() => useActivity("a2"));
    expect(again.result.current).toEqual({ database: "backup" });
    expect(other.result.current).toEqual({});
    let endRestore = () => {};
    act(() => {
      endRestore = trackActivity("a1", "media", "restore");
    });
    expect(again.result.current).toEqual({ database: "backup", media: "restore" });
    act(() => {
      end();
      endRestore();
    });
    expect(again.result.current).toEqual({});
  });
});
