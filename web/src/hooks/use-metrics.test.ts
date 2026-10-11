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
});
