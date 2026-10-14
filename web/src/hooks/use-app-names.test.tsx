import type { ReactNode } from "react";
import { renderHook, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { AppsContext, type App, type AppsContextValue } from "@/context/apps-context-base";
import { useAppNames } from "@/hooks/use-app-names";
import { fakeAPI } from "@/test/fake-api";

const app = (id: string, name: string) => ({ id, name }) as App;

afterEach(() => vi.unstubAllGlobals());

describe("useAppNames", () => {
  it("reads each server's catalog once, the live list on top, and again when that list changes", async () => {
    const catalog: Record<string, { id: string; name: string }[]> = {
      s1: [{ id: "a1", name: "shop" }],
      s2: [{ id: "b1", name: "blog" }],
    };
    const api = fakeAPI({ "/api/v1/app/get-apps": (_: unknown, q: URLSearchParams) => ({ apps: catalog[q.get("server_id") ?? ""] }) });
    let apps = [app("a1", "shop")];
    const wrapper = ({ children }: { children: ReactNode }) => (
      <AppsContext.Provider value={{ apps } as unknown as AppsContextValue}>{children}</AppsContext.Provider>
    );
    const { result, rerender } = renderHook(() => useAppNames(["s2", "s1", "s2"]), { wrapper });
    await waitFor(() => expect(result.current.get("b1")).toBe("blog"));
    expect(api.calls.map((c) => c.query).sort()).toEqual(["?server_id=s1", "?server_id=s2"]);

    // Renamed elsewhere, then the live list changes: read again.
    catalog.s2 = [{ id: "b1", name: "journal" }];
    rerender();
    expect(api.calls).toHaveLength(2);
    apps = [app("a1", "store")];
    rerender();
    expect(result.current.get("a1")).toBe("store");
    await waitFor(() => expect(result.current.get("b1")).toBe("journal"));
    expect(api.calls).toHaveLength(4);
  });

  it("leaves out a server it can't read", async () => {
    fakeAPI({});
    const wrapper = ({ children }: { children: ReactNode }) => (
      <AppsContext.Provider value={{ apps: [] } as unknown as AppsContextValue}>{children}</AppsContext.Provider>
    );
    const { result } = renderHook(() => useAppNames(["s1"]), { wrapper });
    await waitFor(() => expect(result.current.size).toBe(0));
  });
});
