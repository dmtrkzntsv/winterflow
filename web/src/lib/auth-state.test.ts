import { afterEach, describe, expect, it, vi } from "vitest";

import { AUTH_STATE_TIMEOUT_MS, fetchAuthState, loadAuthState, resetAuthStateCache } from "@/lib/auth-state";
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
  it("reads whether the instance is fresh, sign-up is open, its providers and connected apps", async () => {
    fakeAPI({
      "/api/v1/auth/state": {
        bootstrap: true,
        registration_enabled: true,
        oauth_server: true,
        providers: [{ id: "google", name: "Google" }, { id: "oidc", name: "" }, { name: "no id" }, "junk"],
      },
    });
    expect(await fetchAuthState()).toEqual({
      bootstrap: true,
      registrationEnabled: true,
      oauthServer: true,
      providers: [
        { id: "google", name: "Google" },
        { id: "oidc", name: "oidc" },
      ],
    });
  });

  it("reads a missing field as false or empty", async () => {
    fakeAPI({ "/api/v1/auth/state": {} });
    expect(await fetchAuthState()).toEqual({
      bootstrap: false,
      registrationEnabled: false,
      oauthServer: false,
      providers: [],
    });
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

describe("loadAuthState", () => {
  afterEach(() => resetAuthStateCache());

  it("reads the state once", async () => {
    const api = fakeAPI({ "/api/v1/auth/state": { oauth_server: true } });
    await loadAuthState();
    expect((await loadAuthState()).oauthServer).toBe(true);
    expect(api.calls.filter((c) => c.path === "/api/v1/auth/state")).toHaveLength(1);
  });

  it("asks again after a failure", async () => {
    let fail = true;
    fakeAPI({ "/api/v1/auth/state": () => (fail ? new Error("down") : { oauth_server: true }) });
    await expect(loadAuthState()).rejects.toThrow();
    fail = false;
    expect((await loadAuthState()).oauthServer).toBe(true);
  });
});
