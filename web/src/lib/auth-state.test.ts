import { afterEach, describe, expect, it, vi } from "vitest";

import { fetchAuthState } from "@/lib/auth-state";
import { fakeAPI } from "@/test/fake-api";

afterEach(() => vi.unstubAllGlobals());

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
});
