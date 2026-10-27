import { afterEach, describe, expect, it, vi } from "vitest";

import { connectedAppMeta, connectedAppWhere, createAccountApi, providerName, type ConnectedApp } from "@/lib/account";
import { fakeAPI } from "@/test/fake-api";

afterEach(() => vi.unstubAllGlobals());

const APP: ConnectedApp = {
  grant_id: "g",
  client_name: "Claude",
  redirect_host: "claude.ai",
  is_loopback: false,
  created_at: "2026-10-27T10:00:00Z",
  last_used_at: "2026-10-27T11:00:00Z",
};

describe("account api", () => {
  it("reads a null list as empty and posts revokes and unlinks", async () => {
    const api = fakeAPI({
      "/api/v1/user/get-connected-apps": null,
      "/api/v1/user/get-identities": null,
      "/api/v1/user/revoke-connected-app": null,
      "/api/v1/user/unlink-identity": null,
    });
    const a = createAccountApi();
    expect(await a.connectedApps()).toEqual([]);
    expect(await a.identities()).toEqual([]);
    await a.revokeConnectedApp("g1");
    await a.unlinkIdentity("apple");
    expect(api.posted("/api/v1/user/revoke-connected-app")).toEqual([{ grant_id: "g1" }]);
    expect(api.posted("/api/v1/user/unlink-identity")).toEqual([{ provider: "apple" }]);
  });
});

describe("connected app text", () => {
  it("says where it answers", () => {
    expect(connectedAppWhere(APP)).toBe("claude.ai");
    expect(connectedAppWhere({ ...APP, is_loopback: true })).toBe("On your computer");
    expect(connectedAppWhere({ ...APP, redirect_host: "" })).toBe("Unknown address");
  });

  it("says when it was connected and used", () => {
    expect(connectedAppMeta(APP, Date.parse("2026-10-27T12:00:00Z"))).toBe("Connected 2 hours ago · Last used 1 hour ago");
  });
});

describe("providerName", () => {
  it("prefers the instance's name, then a known one, then the id", () => {
    expect(providerName("oidc", [{ id: "oidc", name: "Acme SSO" }])).toBe("Acme SSO");
    expect(providerName("google")).toBe("Google");
    expect(providerName("other")).toBe("other");
  });
});
