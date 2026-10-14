import { afterEach, describe, expect, it, vi } from "vitest";

import { AUTH_STATE_TIMEOUT_MS, fetchAuthState } from "@/lib/auth-state";
import { fakeAPI } from "@/test/fake-api";

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

// hangingFetch never answers; like fetch, it rejects once its signal is
// (or becomes) aborted.
function hangingFetch() {
  const fetchMock = vi.fn(
    (_url: string, init?: RequestInit) =>
      new Promise<Response>((_resolve, reject) => {
        const fail = () => reject(new DOMException("aborted", "AbortError"));
        if (init?.signal?.aborted) fail();
        else init?.signal?.addEventListener("abort", fail);
      }),
  );
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

describe("fetchAuthState", () => {
  it("reads whether the instance is fresh and whether sign-up is open", async () => {
    fakeAPI({ "/api/v1/auth/state": { bootstrap: true, registration_enabled: true } });
    expect(await fetchAuthState()).toEqual({ bootstrap: true, registrationEnabled: true });
  });

  it("reads a missing field as false", async () => {
    fakeAPI({ "/api/v1/auth/state": {} });
    expect(await fetchAuthState()).toEqual({ bootstrap: false, registrationEnabled: false });
  });

  it("fails when the server refuses", async () => {
    fakeAPI({ "/api/v1/auth/state": new Error("failed to read auth state") });
    await expect(fetchAuthState()).rejects.toThrow();
  });

  it("gives up when the server doesn't answer in time", async () => {
    vi.useFakeTimers();
    hangingFetch();
    const read = fetchAuthState();
    const outcome = expect(read).rejects.toThrow("aborted");
    await vi.advanceTimersByTimeAsync(AUTH_STATE_TIMEOUT_MS);
    await outcome;
  });

  it("stops when the caller aborts", async () => {
    hangingFetch();
    const ctl = new AbortController();
    const read = fetchAuthState(ctl.signal);
    ctl.abort();
    await expect(read).rejects.toThrow("aborted");
  });

  it("fails at once for an already aborted caller", async () => {
    const fetchMock = hangingFetch();
    const ctl = new AbortController();
    ctl.abort();
    await expect(fetchAuthState(ctl.signal)).rejects.toThrow("aborted");
    expect((fetchMock.mock.calls[0]?.[1] as RequestInit).signal?.aborted).toBe(true);
  });
});
