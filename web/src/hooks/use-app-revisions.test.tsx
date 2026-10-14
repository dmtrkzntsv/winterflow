import type { ReactNode } from "react";
import { act, renderHook, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { AppsContext, type AppRevisions, type AppsContextValue } from "@/context/apps-context-base";
import { useAppRevisions } from "@/hooks/use-app-revisions";

const revs = (hash: string): AppRevisions => ({ current: hash, deployed: hash, revisions: [{ hash, subject: `at ${hash}`, timestamp: 1 }] });

function wrap(getRevisions: AppsContextValue["getRevisions"]) {
  const value = { getRevisions } as unknown as AppsContextValue;
  return ({ children }: { children: ReactNode }) => <AppsContext.Provider value={value}>{children}</AppsContext.Provider>;
}

describe("useAppRevisions", () => {
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
});
