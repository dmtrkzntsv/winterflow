import { describe, expect, it, vi } from "vitest";

vi.mock("@/lib/ecies", () => ({
  encryptSecret: vi.fn(async (value: string, key: string) => `ct(${key}):${value}`),
}));

import {
  applySyncEvent,
  coverageLabel,
  createSharedVariablesApi,
  overrideNote,
  pushSummary,
  serverNameMap,
  sharedNames,
  variableNameError,
  type ServerVariables,
  type SharedVariable,
} from "@/lib/shared-variables";

const secret: SharedVariable = {
  id: "v1", name: "SMTP_PASS", scope: "org", is_secret: true, value: "<encrypted>",
  description: "", updated_at: "2026-10-11T00:00:00Z", updated_by: "u1", missing_on: [],
};

describe("helpers", () => {
  it("validates names like the API", () => {
    expect(variableNameError("SMTP_HOST")).toBe("");
    expect(variableNameError("")).toMatch(/required/);
    expect(variableNameError("1X")).toMatch(/letters, digits/);
    expect(variableNameError("WF_APP_ID")).toMatch(/reserved/);
    expect(variableNameError("COMPOSE_PROJECT_NAME")).toMatch(/reserved/);
    expect(variableNameError("A".repeat(256))).toMatch(/255/);
  });

  it("labels coverage by server name", () => {
    const names = new Map([["s3", "srv-3"]]);
    expect(coverageLabel(secret, names)).toBe("All servers");
    expect(coverageLabel({ ...secret, missing_on: ["s3", "s9"] }, names)).toBe("Missing on srv-3, s9");
    expect(coverageLabel({ ...secret, is_secret: false }, names)).toBe("");
  });

  it("summarizes a push", () => {
    expect(pushSummary(3, 1)).toBe("Will be pushed to 3 servers (1 pending, no key yet)");
    expect(pushSummary(1, 0)).toBe("Will be pushed to 1 server");
  });

  it("overlays sync events per server", () => {
    let s = applySyncEvent({}, { type: "vars.sync_failed", payload: { server_id: "a", error: "x" } });
    expect(s).toEqual({ a: "failed" });
    s = applySyncEvent(s, { type: "vars.synced", payload: { server_id: "a", revision: 2 } });
    expect(s).toEqual({ a: "synced" });
    expect(applySyncEvent(s, { type: "apps_status", payload: { server_id: "a" } })).toBe(s);
  });

  it("maps server names from a key listing", () => {
    const m = serverNameMap({ servers: [{ server_id: "a", name: "alpha", public_key: "K" }], pending_servers: [{ server_id: "p", name: "new" }] });
    expect(m.get("a")).toBe("alpha");
    expect(m.get("p")).toBe("new");
  });

  it("lists the shared names an app sees, never values", () => {
    const v: ServerVariables = {
      variables: [{ ...secret, id: "s1", name: "LOCAL", scope: "server", is_secret: false, value: "x" }],
      inherited: [
        { ...secret, name: "LOCAL", is_secret: false, value: "org", overridden: true },
        { ...secret, overridden: false },
      ],
      builtins: [{ name: "WF_TZ", value: "", description: "tz" }],
      missing_org_secrets: [],
      sync: { server_id: "s", name: "s", has_key: true, revision: 1, agent_revision: 1, state: "synced" },
    };
    const names = sharedNames(v);
    expect(names).toEqual([
      { name: "LOCAL", scope: "server" },
      { name: "SMTP_PASS", scope: "org" },
      { name: "WF_TZ", scope: "builtin" },
    ]);
    expect(overrideNote("LOCAL", names)).toBe("overrides server value");
    expect(overrideNote("SMTP_PASS", names)).toBe("overrides org value");
    expect(overrideNote("WF_TZ", names)).toBe("");
    expect(overrideNote("OTHER", names)).toBe("");
  });
});

describe("createSharedVariablesApi", () => {
  function fakeFetch(data: unknown, status = 200) {
    return vi.fn<typeof fetch>(async () =>
      new Response(JSON.stringify(status === 200 ? { success: true, data } : { success: false, message: "nope" }), { status }),
    );
  }

  it("calls each route with the documented body", async () => {
    const f = fakeFetch({ servers: ["a"] });
    const api = createSharedVariablesApi(f);
    await api.saveServer("srv 1", { name: "X", is_secret: false, value: "1", description: "" });
    await api.deleteServer("srv 1", "v1", true);
    await api.deleteOrg("v2");
    expect(await api.resync({})).toEqual(["a"]);
    expect(await api.reload({ server_id: "a", names: ["X"] })).toEqual(["a"]);
    await api.listServer("srv 1");
    const calls = f.mock.calls.map(([url, init]) => [new URL(String(url)).pathname + new URL(String(url)).search, init?.body ? JSON.parse(String(init.body)) : undefined]);
    expect(calls).toEqual([
      ["/api/v1/server/save-shared-variable", { name: "X", is_secret: false, value: "1", description: "", server_id: "srv 1" }],
      ["/api/v1/server/delete-shared-variable", { server_id: "srv 1", id: "v1", reload: true }],
      ["/api/v1/org/delete-shared-variable", { id: "v2" }],
      ["/api/v1/shared-variables/resync", {}],
      ["/api/v1/shared-variables/reload", { server_id: "a", names: ["X"] }],
      ["/api/v1/server/list-shared-variables?server_id=srv%201", undefined],
    ]);
    expect(f.mock.calls[0][1]?.credentials).toBe("include");
  });

  it("secret saves omit value and forward ciphertexts; plain saves send value", async () => {
    const f = fakeFetch({});
    const api = createSharedVariablesApi(f);
    await api.saveOrg({ name: "S", is_secret: true, description: "", ciphertexts: { a: "ct" } });
    await api.saveOrg({ name: "P", is_secret: false, value: "v", description: "" });
    // @ts-expect-error a secret save cannot carry a value
    await api.saveOrg({ name: "Bad", is_secret: true, value: "x", description: "" });
    const bodies = f.mock.calls.map(([, init]) => JSON.parse(String(init?.body)));
    expect(bodies[0]).toEqual({ name: "S", is_secret: true, description: "", ciphertexts: { a: "ct" } });
    expect("value" in bodies[0]).toBe(false);
    expect(bodies[1].value).toBe("v");
  });

  it("surfaces the API's refusal message", async () => {
    const api = createSharedVariablesApi(fakeFetch(null, 400));
    await expect(api.saveOrg({ name: "X", is_secret: false, value: "1", description: "" })).rejects.toThrow("nope");
  });
});
