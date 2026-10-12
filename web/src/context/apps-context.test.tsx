import type { ReactNode } from "react";
import { renderHook, waitFor as waitUntil } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { AppsProvider } from "@/context/apps-context";
import { useApps } from "@/context/use-apps";

vi.mock("@/context/use-auth", () => ({ useAuth: () => ({ isAuthenticated: true }) }));
vi.mock("@/context/use-servers", () => ({ useServers: () => ({ activeServerId: "srv-1" }) }));
const waitFor = vi.fn();
vi.mock("@/context/use-notifications", () => ({
  useNotifications: () => ({ subscribe: () => () => {}, waitFor, connected: true }),
}));

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status });

// The API refuses a save whose compose file references a storage the server
// doesn't have with a 400 and the reason.
const refusal = "unresolved storage variable: ${WF_STORAGE_NOPE_PATH}: there is no storage \"nope\" on this server";

const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
  const url = String(input);
  if (url.includes("/api/v1/app/get-apps?")) return json(200, { data: { apps: [] } });
  if (url.includes("/api/v1/app/get-apps-status")) return json(200, { data: { apps: [] } });
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
