import { renderHook, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { useHostCheck } from "./use-host-check";

type Pending = {
  domain: string;
  signal?: AbortSignal;
  answer: (data: unknown, status?: number) => void;
  fail: () => void;
};

// stubChecks answers each domains/check fetch when the test says so.
function stubChecks() {
  const pending: Pending[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(
      (url: string, init?: RequestInit) =>
        new Promise<Response>((resolve, reject) => {
          pending.push({
            domain: new URL(url, "http://x").searchParams.get("domain") ?? "",
            signal: init?.signal ?? undefined,
            answer: (data, status = 200) => resolve(new Response(JSON.stringify({ data }), { status })),
            fail: () => reject(new TypeError("Failed to fetch")),
          });
        }),
    ),
  );
  return pending;
}

const TAKEN = { available: false, covered: true, claims: [{ app_name: "blog" }], registration: null };

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("useHostCheck", () => {
  it("returns the answer while the host is the one it was for, aborting older checks", async () => {
    const pending = stubChecks();
    const onResult = vi.fn();
    const { result, rerender } = renderHook(
      ({ host }) => useHostCheck("s1", "a1", host, { enabled: true, delay: 0, onResult }),
      { initialProps: { host: "a.example.com" } },
    );
    await waitFor(() => expect(pending).toHaveLength(1));
    rerender({ host: "b.example.com" });
    expect(pending[0].signal?.aborted).toBe(true);
    await waitFor(() => expect(pending).toHaveLength(2));
    expect(pending.map((p) => p.domain)).toEqual(["a.example.com", "b.example.com"]);
    pending[1].answer(TAKEN);
    await waitFor(() => expect(result.current).toEqual({ issue: { kind: "taken", app: "blog" }, registration: undefined }));
    expect(onResult).toHaveBeenCalledTimes(1);

    // Another host: the old answer no longer shows.
    rerender({ host: "c.example.com" });
    expect(result.current).toBeNull();
  });

  it("asks again on recheck, reads the delay when scheduling, and shows nothing for a refused or failed check", async () => {
    const pending = stubChecks();
    const delay = vi.fn(() => 0);
    const { result, rerender } = renderHook(
      ({ recheck }) => useHostCheck("s1", undefined, "a.example.com", { enabled: true, delay, recheck }),
      { initialProps: { recheck: 0 } },
    );
    await waitFor(() => expect(pending).toHaveLength(1));
    expect(delay).toHaveBeenCalledTimes(1);
    pending[0].answer(null, 500);
    rerender({ recheck: 1 });
    await waitFor(() => expect(pending).toHaveLength(2));
    expect(delay).toHaveBeenCalledTimes(2);
    pending[1].fail();
    await new Promise((r) => setTimeout(r, 10));
    expect(result.current).toBeNull();
  });

  it("checks nothing while disabled", async () => {
    const pending = stubChecks();
    renderHook(() => useHostCheck("s1", undefined, "a.example.com", { enabled: false, delay: 0 }));
    await new Promise((r) => setTimeout(r, 10));
    expect(pending).toHaveLength(0);
  });
});
