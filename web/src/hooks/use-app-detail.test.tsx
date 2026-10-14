import type { ReactNode } from "react";
import { renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { AppsContext, type AppDetailPayload, type AppsContextValue } from "@/context/apps-context-base";
import { forgetAppDetail, forgetAppDetails, readAppDetail, seedAppDetail, useAppDetail } from "@/hooks/use-app-detail";

const detail = (id: string): AppDetailPayload => ({
  app: { app_id: id, config: "", files: [], variables: [] },
  revision: 1,
  available_revisions: null,
});

beforeEach(() => forgetAppDetails());

describe("readAppDetail", () => {
  it("reads an app version once, afresh when asked, and again for a new version", async () => {
    const getApp = vi.fn(async (id: string) => detail(id));
    const first = readAppDetail(getApp, "a1", "3");
    expect(readAppDetail(getApp, "a1", "3")).toBe(first);
    expect(getApp).toHaveBeenCalledTimes(1);
    // A fresh read replaces it: the next shared read gets that one.
    const fresh = readAppDetail(getApp, "a1", "3", true);
    expect(fresh).not.toBe(first);
    expect(readAppDetail(getApp, "a1", "3")).toBe(fresh);
    readAppDetail(getApp, "a1", "4");
    expect(getApp).toHaveBeenCalledTimes(3);
  });

  it("keeps one app's read and no failed one", async () => {
    const getApp = vi.fn(async (id: string) => (id === "bad" ? Promise.reject(new Error("offline")) : detail(id)));
    readAppDetail(getApp, "a1", "1");
    readAppDetail(getApp, "a2", "1");
    readAppDetail(getApp, "a1", "1");
    expect(getApp).toHaveBeenCalledTimes(3);
    await expect(readAppDetail(getApp, "bad", "1")).rejects.toThrow("offline");
    await expect(readAppDetail(getApp, "bad", "1")).rejects.toThrow("offline");
    expect(getApp).toHaveBeenCalledTimes(5);
  });
});

describe("an app without a version", () => {
  it("is read afresh every time and keeps nothing (the API stores no version after a save)", async () => {
    const getApp = vi.fn(async (id: string) => detail(id));
    readAppDetail(getApp, "a1", "3");
    readAppDetail(getApp, "a1", "");
    readAppDetail(getApp, "a1", "");
    expect(getApp).toHaveBeenCalledTimes(3);
    // The read of version 3 went with the versionless one.
    readAppDetail(getApp, "a1", "3");
    expect(getApp).toHaveBeenCalledTimes(4);
  });

  it("is never seeded; a version is, and a forget drops it", async () => {
    const getApp = vi.fn(async (id: string) => detail(id));
    const seeded = detail("a1");
    seedAppDetail("a1", "", seeded);
    seedAppDetail("a1", undefined, seeded);
    await readAppDetail(getApp, "a1", "");
    expect(getApp).toHaveBeenCalledTimes(1);
    seedAppDetail("a1", "5", seeded);
    expect(await readAppDetail(getApp, "a1", "5")).toBe(seeded);
    expect(getApp).toHaveBeenCalledTimes(1);
    forgetAppDetail("a1");
    await readAppDetail(getApp, "a1", "5");
    expect(getApp).toHaveBeenCalledTimes(2);
  });
});

describe("useAppDetail", () => {
  const setup = (getApp: AppsContextValue["getApp"]) => {
    const value = { getApp } as unknown as AppsContextValue;
    const wrapper = ({ children }: { children: ReactNode }) => <AppsContext.Provider value={value}>{children}</AppsContext.Provider>;
    return renderHook(({ version, revisit }: { version: string; revisit: number }) => useAppDetail("a1", version, revisit), {
      wrapper,
      initialProps: { version: "1", revisit: 0 },
    });
  };

  it("shares a read of the version asked for, re-reads on a revisit, and drops it when the app is left", async () => {
    const getApp = vi.fn(async (id: string) => detail(id));
    // Configuration asked for version 1 first.
    const configuration = readAppDetail(getApp, "a1", "1");
    const { result, rerender, unmount } = setup(getApp);
    await waitFor(() => expect(result.current.detail).not.toBeNull());
    expect(getApp).toHaveBeenCalledTimes(1);
    expect(result.current.detail).toBe(await configuration);

    rerender({ version: "1", revisit: 1 });
    await waitFor(() => expect(getApp).toHaveBeenCalledTimes(2));
    // A new version is a new read, shared again.
    rerender({ version: "2", revisit: 1 });
    await waitFor(() => expect(getApp).toHaveBeenCalledTimes(3));
    readAppDetail(getApp, "a1", "2");
    expect(getApp).toHaveBeenCalledTimes(3);

    unmount();
    readAppDetail(getApp, "a1", "2");
    expect(getApp).toHaveBeenCalledTimes(4);
  });
});
