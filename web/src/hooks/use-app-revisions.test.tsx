import type { ReactNode } from "react";
import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { AppsContext, type AppRevisions, type AppsContextValue } from "@/context/apps-context-base";
import { useAppRevisions } from "@/hooks/use-app-revisions";
import { forgetRevisions, resetRevisionsCache } from "@/lib/revisions";

const revs = (hash: string): AppRevisions => ({ current: hash, deployed: hash, revisions: [{ hash, subject: `at ${hash}`, timestamp: 1 }] });

function wrap(getRevisions: AppsContextValue["getRevisions"]) {
  const value = { getRevisions } as unknown as AppsContextValue;
  return ({ children }: { children: ReactNode }) => <AppsContext.Provider value={value}>{children}</AppsContext.Provider>;
}

describe("useAppRevisions", () => {
  beforeEach(resetRevisionsCache);

  it("reads the history, and again on reload", async () => {
    const getRevisions = vi.fn(async (id: string) => revs(id));
    const { result } = renderHook(() => useAppRevisions("a1"), { wrapper: wrap(getRevisions) });
    await waitFor(() => expect(result.current.data?.current).toBe("a1"));
    await act(() => result.current.reload());
    expect(getRevisions).toHaveBeenCalledTimes(2);
    expect(result.current.loading).toBe(false);
  });

  it("reads nothing until enabled, and reports a failed read", async () => {
    const getRevisions = vi.fn(async () => Promise.reject(new Error("agent offline")));
    const { result, rerender } = renderHook(({ on }) => useAppRevisions("a1", on), {
      wrapper: wrap(getRevisions as unknown as AppsContextValue["getRevisions"]),
      initialProps: { on: false },
    });
    expect(getRevisions).not.toHaveBeenCalled();
    rerender({ on: true });
    await waitFor(() => expect(result.current.error).toBe("agent offline"));
    expect(result.current.loading).toBe(false);
  });

  it("drops a late answer for the app it no longer shows", async () => {
    let first: (v: AppRevisions) => void = () => {};
    const getRevisions = vi.fn((id: string) =>
      id === "a1" ? new Promise<AppRevisions>((resolve) => (first = resolve)) : Promise.resolve(revs(id)),
    );
    const { result, rerender } = renderHook(({ id }) => useAppRevisions(id), {
      wrapper: wrap(getRevisions as unknown as AppsContextValue["getRevisions"]),
      initialProps: { id: "a1" },
    });
    rerender({ id: "a2" });
    await waitFor(() => expect(result.current.data?.current).toBe("a2"));
    await act(async () => first(revs("a1")));
    expect(result.current.data?.current).toBe("a2");
  });

  it("is loading from the moment the app's history is forgotten, until the new read lands", async () => {
    let next: (v: AppRevisions) => void = () => {};
    let calls = 0;
    const getRevisions = vi.fn(() => (++calls === 1 ? Promise.resolve(revs("one")) : new Promise<AppRevisions>((resolve) => (next = resolve))));
    const { result } = renderHook(() => useAppRevisions("a1"), { wrapper: wrap(getRevisions as unknown as AppsContextValue["getRevisions"]) });
    await waitFor(() => expect(result.current.loading).toBe(false));
    act(() => forgetRevisions("a1"));
    expect(result.current.loading).toBe(true);
    await act(async () => next(revs("two")));
    expect(result.current.loading).toBe(false);
    expect(result.current.data?.current).toBe("two");
  });

  it("is loading again from the moment it is enabled again, until the new read lands", async () => {
    let next: (v: AppRevisions) => void = () => {};
    let calls = 0;
    const getRevisions = vi.fn(() => (++calls === 1 ? Promise.resolve(revs("one")) : new Promise<AppRevisions>((resolve) => (next = resolve))));
    const { result, rerender } = renderHook(({ on }) => useAppRevisions("a1", on), {
      wrapper: wrap(getRevisions as unknown as AppsContextValue["getRevisions"]),
      initialProps: { on: true },
    });
    await waitFor(() => expect(result.current.loading).toBe(false));
    rerender({ on: false });
    expect(result.current.loading).toBe(false);
    rerender({ on: true });
    expect(result.current.loading).toBe(true);
    await act(async () => next(revs("two")));
    expect(result.current.loading).toBe(false);
    expect(result.current.data?.current).toBe("two");
  });

  it("never shows the previous app's history while the next one loads", async () => {
    let second: (v: AppRevisions) => void = () => {};
    const getRevisions = vi.fn((id: string) =>
      id === "a2" ? new Promise<AppRevisions>((resolve) => (second = resolve)) : Promise.resolve(revs(id)),
    );
    const { result, rerender } = renderHook(({ id }) => useAppRevisions(id), {
      wrapper: wrap(getRevisions as unknown as AppsContextValue["getRevisions"]),
      initialProps: { id: "a1" },
    });
    await waitFor(() => expect(result.current.data?.current).toBe("a1"));
    rerender({ id: "a2" });
    expect(result.current.data).toBeNull();
    expect(result.current.loading).toBe(true);
    await act(async () => second(revs("a2")));
    expect(result.current.data?.current).toBe("a2");
  });

  describe("with a cache key", () => {
    it("serves a remount from memory for the same app and key", async () => {
      const getRevisions = vi.fn(async (id: string) => revs(id));
      const first = renderHook(() => useAppRevisions("a1", true, "3"), { wrapper: wrap(getRevisions) });
      await waitFor(() => expect(first.result.current.data?.current).toBe("a1"));
      first.unmount();
      const second = renderHook(() => useAppRevisions("a1", true, "3"), { wrapper: wrap(getRevisions) });
      await waitFor(() => expect(second.result.current.data?.current).toBe("a1"));
      expect(getRevisions).toHaveBeenCalledTimes(1);
    });

    it("reads again when the key changes or another app asks", async () => {
      const getRevisions = vi.fn(async (id: string) => revs(id));
      const { rerender, result } = renderHook(({ id, key }) => useAppRevisions(id, true, key), {
        wrapper: wrap(getRevisions),
        initialProps: { id: "a1", key: "3" },
      });
      await waitFor(() => expect(result.current.data?.current).toBe("a1"));
      rerender({ id: "a1", key: "4" });
      await waitFor(() => expect(getRevisions).toHaveBeenCalledTimes(2));
      rerender({ id: "a2", key: "4" });
      await waitFor(() => expect(result.current.data?.current).toBe("a2"));
      expect(getRevisions).toHaveBeenCalledTimes(3);
    });

    it("reload always asks the agent, and an uncached read forgets what was kept", async () => {
      const getRevisions = vi.fn(async (id: string) => revs(id));
      const keyed = renderHook(() => useAppRevisions("a1", true, "3"), { wrapper: wrap(getRevisions) });
      await waitFor(() => expect(keyed.result.current.data).not.toBeNull());
      await act(() => keyed.result.current.reload());
      expect(getRevisions).toHaveBeenCalledTimes(2);
      keyed.unmount();
      // The History sheet reads without a key: what it saw may differ from what was kept.
      const plain = renderHook(() => useAppRevisions("a1"), { wrapper: wrap(getRevisions) });
      await waitFor(() => expect(plain.result.current.data).not.toBeNull());
      plain.unmount();
      const again = renderHook(() => useAppRevisions("a1", true, "3"), { wrapper: wrap(getRevisions) });
      await waitFor(() => expect(again.result.current.data).not.toBeNull());
      expect(getRevisions).toHaveBeenCalledTimes(4);
    });

    it("does not keep a failed read", async () => {
      const getRevisions = vi.fn().mockRejectedValueOnce(new Error("offline")).mockResolvedValue(revs("a1"));
      const first = renderHook(() => useAppRevisions("a1", true, "3"), {
        wrapper: wrap(getRevisions as unknown as AppsContextValue["getRevisions"]),
      });
      await waitFor(() => expect(first.result.current.error).toBe("offline"));
      first.unmount();
      const second = renderHook(() => useAppRevisions("a1", true, "3"), {
        wrapper: wrap(getRevisions as unknown as AppsContextValue["getRevisions"]),
      });
      await waitFor(() => expect(second.result.current.data?.current).toBe("a1"));
    });
  });
});
