import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  METRICS_REFRESH_MS,
  useAppMetrics,
  useMetricsOverview,
  useServerMetrics,
} from "@/hooks/use-metrics";

let urls: string[] = [];
const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
  urls.push(String(input));
  return new Response(JSON.stringify({ data: { interval_seconds: 60, points: [], apps: [] } }), {
    status: 200,
  });
});

describe("metrics hooks", () => {
  beforeEach(() => {
    urls = [];
    fetchMock.mockClear();
    vi.stubGlobal("fetch", fetchMock);
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it("loads a server's series with its range", async () => {
    const { result } = renderHook(() => useServerMetrics("srv-1", "24h"));
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(urls[0]).toContain("/api/v1/metrics/server?server_id=srv-1&range=24h");
    expect(result.current.data?.interval_seconds).toBe(60);
    expect(result.current.error).toBeNull();
  });

  it("does nothing until a server and app are known", () => {
    const { result } = renderHook(() => useAppMetrics("srv-1", null, "1h"));
    expect(fetchMock).not.toHaveBeenCalled();
    expect(result.current.loading).toBe(false);
  });

  it("loads an app's series", async () => {
    const { result } = renderHook(() => useAppMetrics("srv-1", "app-1", "7d"));
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(urls[0]).toContain("/api/v1/metrics/app?server_id=srv-1&app_id=app-1&range=7d");
  });

  it("refreshes every minute while visible and surfaces errors", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const { result } = renderHook(() => useMetricsOverview());
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
    expect(urls[0]).toContain("/api/v1/metrics/overview");

    fetchMock.mockResolvedValueOnce(
      new Response(JSON.stringify({ success: false, message: "forbidden" }), { status: 403 }),
    );
    await act(async () => {
      vi.advanceTimersByTime(METRICS_REFRESH_MS);
    });
    await waitFor(() => expect(result.current.error).toBe("forbidden"));

    // Hidden tab: the timer fires but nothing is fetched.
    const visibility = vi.spyOn(document, "visibilityState", "get").mockReturnValue("hidden");
    const before = fetchMock.mock.calls.length;
    await act(async () => {
      vi.advanceTimersByTime(METRICS_REFRESH_MS);
    });
    expect(fetchMock.mock.calls.length).toBe(before);
    visibility.mockRestore();
  });

  it("ignores a late response from the previous path and resets state", async () => {
    let releaseOld: (r: Response) => void = () => {};
    fetchMock.mockImplementationOnce(
      () => new Promise<Response>((resolve) => (releaseOld = resolve)),
    );
    const { result, rerender } = renderHook(({ range }) => useServerMetrics("srv-1", range), {
      initialProps: { range: "1h" as "1h" | "24h" },
    });
    expect(result.current.loading).toBe(true);
    rerender({ range: "24h" });
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.data?.interval_seconds).toBe(60);

    await act(async () => {
      releaseOld(
        new Response(JSON.stringify({ data: { interval_seconds: 999, points: [], apps: [] } })),
      );
    });
    expect(result.current.data?.interval_seconds).toBe(60);

    // Switching path clears old data and starts loading again.
    let releaseNext: (r: Response) => void = () => {};
    fetchMock.mockImplementationOnce(
      () => new Promise<Response>((resolve) => (releaseNext = resolve)),
    );
    rerender({ range: "1h" });
    expect(result.current.data).toBeNull();
    expect(result.current.loading).toBe(true);
    releaseNext(new Response(JSON.stringify({ data: { interval_seconds: 1, points: [], apps: [] } })));
  });

  it("clears state when the path becomes null", async () => {
    const { result, rerender } = renderHook(({ app }) => useAppMetrics("srv-1", app, "1h"), {
      initialProps: { app: "app-1" as string | null },
    });
    await waitFor(() => expect(result.current.data).not.toBeNull());
    rerender({ app: null });
    expect(result.current.data).toBeNull();
    expect(result.current.loading).toBe(false);
    expect(result.current.error).toBeNull();
  });

  it("cleans up on unmount and does not set state afterwards", async () => {
    const removeSpy = vi.spyOn(document, "removeEventListener");
    const clearSpy = vi.spyOn(window, "clearInterval");
    const errorSpy = vi.spyOn(console, "error").mockImplementation(() => {});
    let release: (r: Response) => void = () => {};
    fetchMock.mockImplementationOnce(() => new Promise<Response>((resolve) => (release = resolve)));
    const { unmount } = renderHook(() => useMetricsOverview());
    unmount();
    expect(removeSpy).toHaveBeenCalledWith("visibilitychange", expect.any(Function));
    expect(clearSpy).toHaveBeenCalled();
    release(new Response(JSON.stringify({ data: { servers: null } })));
    await new Promise((r) => setTimeout(r, 0));
    expect(errorSpy).not.toHaveBeenCalled();
    removeSpy.mockRestore();
    clearSpy.mockRestore();
    errorSpy.mockRestore();
  });

  it("normalizes null slices to arrays", async () => {
    fetchMock.mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          data: { servers: [{ server_id: "s", sparkline: null, liveness: "unknown", latest: null }] },
        }),
      ),
    );
    const { result } = renderHook(() => useMetricsOverview());
    await waitFor(() => expect(result.current.data).not.toBeNull());
    expect(result.current.data?.servers?.[0].sparkline).toEqual([]);
  });
});
