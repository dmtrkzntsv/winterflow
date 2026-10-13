import { afterEach, describe, expect, it, vi } from "vitest";

vi.mock("@/lib/ecies", () => ({
  ENCRYPTED: "<encrypted>",
  encryptSecret: vi.fn(async (value: string, key: string) => `ct(${key}):${value}`),
}));

import { WAIT_TIMEOUT } from "@/context/notifications-context-base";
import { fakeAPI } from "@/test/fake-api";
import {
  ApiError,
  createTemplatesApi,
  INSTANCE_WAIT_MS,
  instancesLabel,
  isManagedByTemplate,
  sealSecrets,
} from "@/lib/templates";

const ok = vi.fn(async () => ({ status: 0 }));

afterEach(() => {
  vi.unstubAllGlobals();
  ok.mockClear();
});

describe("createTemplatesApi", () => {
  it("reads templates, revisions and instances", async () => {
    const api = fakeAPI({
      "/api/v1/template/get-templates": { templates: null },
      "/api/v1/template/get-template": { id: "t1", revision: 2 },
      "/api/v1/instance/get-instances": { instances: [{ key: "k1" }] },
      "/api/v1/instance/get-instance": { key: "k1" },
    });
    const t = createTemplatesApi(ok);
    expect(await t.listTemplates()).toEqual([]);
    expect(await t.getTemplate("t1", 2)).toEqual({ id: "t1", revision: 2 });
    await t.getTemplate("t1");
    expect(await t.listInstances({ templateId: "t1" })).toEqual([{ key: "k1" }]);
    await t.listInstances({});
    expect(await t.getInstance("s1", "k1")).toEqual({ key: "k1" });
    expect(api.calls.map((c) => c.query)).toEqual([
      "",
      "?id=t1&revision=2",
      "?id=t1",
      "?template_id=t1",
      "",
      "?server_id=s1&key=k1",
    ]);
  });

  it("passes a read's abort signal to fetch", async () => {
    const api = fakeAPI({
      "/api/v1/template/get-templates": { templates: [] },
      "/api/v1/template/get-template": { id: "t1" },
    });
    const t = createTemplatesApi(ok);
    const ctl = new AbortController();
    await t.listTemplates(ctl.signal);
    await t.getTemplate("t1", undefined, ctl.signal);
    expect(api.fetchMock.mock.calls.map(([, init]) => init?.signal)).toEqual([ctl.signal, ctl.signal]);
  });

  it("saves and deletes templates, keeping the status of a refusal", async () => {
    const conflict = new ApiError("someone saved the template meanwhile", 409);
    const api = fakeAPI({
      "/api/v1/template/save-template": (b: unknown) =>
        (b as { base_revision: number }).base_revision === 1 ? conflict : { id: "t1", name: "shop", revision: 3, warnings: null },
      "/api/v1/template/delete-template": null,
    });
    const t = createTemplatesApi(ok);
    const body = { files: {}, parameters: [], app_name: "x", icon: "", color: "", ingress: [], backups: {} };
    expect((await t.saveTemplate({ id: "t1", name: "shop", body, base_revision: 2 })).revision).toBe(3);
    const err = await t.saveTemplate({ id: "t1", name: "shop", body, base_revision: 1 }).catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect((err as ApiError).status).toBe(409);
    await t.deleteTemplate("t1");
    expect(api.posted("/api/v1/template/delete-template")).toEqual([{ id: "t1" }]);
  });

  it("waits for an instance command's result over SSE", async () => {
    const fetchImpl = vi.fn(async () =>
      new Response(JSON.stringify({ data: { request_id: "r1" } }), { status: 202 }),
    ) as unknown as typeof fetch;
    const waitFor = vi.fn(async () => ({ status: 0 }));
    const t = createTemplatesApi(waitFor, fetchImpl);
    await t.createInstance({ server_id: "s1", key: "k1", template_id: "t1" });
    expect(waitFor).toHaveBeenCalledWith("r1", INSTANCE_WAIT_MS);

    waitFor.mockResolvedValueOnce({ status: 1, error: "image not found" } as never);
    await expect(t.updateInstance({ server_id: "s1", key: "k1" })).rejects.toThrow("image not found");
    waitFor.mockResolvedValueOnce({ status: 1 } as never);
    await expect(t.upgradeInstance({ server_id: "s1", key: "k1", revision: 2 })).rejects.toThrow(
      "The agent reported a failure",
    );
  });

  describe("when the SSE wait times out, reads the instance instead", () => {
    // accepted answers the command with a 202 and get-instance with inst.
    function accepted(inst: unknown) {
      const calls: string[] = [];
      const fetchImpl = vi.fn(async (url: string) => {
        calls.push(url);
        if (url.includes("get-instance")) {
          return inst instanceof Error
            ? new Response(JSON.stringify({ message: inst.message }), { status: 500 })
            : new Response(JSON.stringify({ data: inst }));
        }
        return new Response(JSON.stringify({ data: { request_id: "r1" } }), { status: 202 });
      }) as unknown as typeof fetch;
      const timeout = vi.fn(async () => {
        throw new Error(WAIT_TIMEOUT);
      });
      return { t: createTemplatesApi(timeout, fetchImpl), calls };
    }
    const later = () => new Date(Date.now() + 60_000).toISOString();
    const earlier = "2026-01-01T00:00:00Z";

    it("resolves when the instance was created after the command was sent", async () => {
      const { t, calls } = accepted({ state: "created", last_error: "", updated_at: later() });
      await t.updateInstance({ server_id: "s1", key: "k1" });
      expect(calls.at(-1)).toContain("/api/v1/instance/get-instance?server_id=s1&key=k1");
    });

    it("resolves a delete when the instance was deleted after it was sent", async () => {
      const { t } = accepted({ state: "deleted", last_error: "", updated_at: later() });
      await t.deleteInstance("s1", "k1");
      const stale = accepted({ state: "created", last_error: "", updated_at: later() });
      await expect(stale.t.deleteInstance("s1", "k1")).rejects.toThrow(WAIT_TIMEOUT);
    });

    it("throws the error the command recorded", async () => {
      const { t } = accepted({ state: "pending", last_error: "pull access denied", updated_at: later() });
      await expect(t.createInstance({ server_id: "s1", key: "k1", template_id: "t1" })).rejects.toThrow(
        "pull access denied",
      );
    });

    it("rethrows the timeout when nothing changed since, or the read fails", async () => {
      for (const inst of [
        { state: "created", last_error: "", updated_at: earlier },
        { state: "pending", last_error: "an earlier attempt's error", updated_at: earlier },
        new Error("database down"),
      ]) {
        const { t } = accepted(inst);
        await expect(t.upgradeInstance({ server_id: "s1", key: "k1", revision: 2 })).rejects.toThrow(WAIT_TIMEOUT);
      }
    });

    it("passes any other wait failure through without a read", async () => {
      const fetchImpl = vi.fn(async () =>
        new Response(JSON.stringify({ data: { request_id: "r1" } }), { status: 202 }),
      ) as unknown as typeof fetch;
      const t = createTemplatesApi(vi.fn(async () => Promise.reject(new Error("stream closed"))), fetchImpl);
      await expect(t.updateInstance({ server_id: "s1", key: "k1" })).rejects.toThrow("stream closed");
      expect(fetchImpl).toHaveBeenCalledTimes(1);
    });
  });

  it("returns at once when nothing ran, and refuses a 202 without a request id", async () => {
    const api = fakeAPI({ "/api/v1/instance/create-instance": { instance: { key: "k1" }, request_id: "" } });
    const waitFor = vi.fn();
    await createTemplatesApi(waitFor).createInstance({ server_id: "s1", key: "k1", template_id: "t1" });
    expect(waitFor).not.toHaveBeenCalled();
    expect(api.posted("/api/v1/instance/create-instance")).toEqual([{ server_id: "s1", key: "k1", template_id: "t1" }]);

    const bare = vi.fn(async () => new Response(JSON.stringify({ data: {} }), { status: 202 })) as unknown as typeof fetch;
    await expect(createTemplatesApi(waitFor, bare).unlinkInstance("s1", "k1")).rejects.toThrow("No request id");
  });

  it("carries the API's message on a refused instance command", async () => {
    fakeAPI({ "/api/v1/instance/unlink-instance": new ApiError("instance not found", 404) });
    const err = await createTemplatesApi(ok).unlinkInstance("s1", "k1").catch((e: unknown) => e);
    expect((err as ApiError).message).toBe("instance not found");
    expect((err as ApiError).status).toBe(404);
  });
});

describe("isManagedByTemplate", () => {
  it("spots the API's managed-app conflict", () => {
    expect(isManagedByTemplate("this app is managed by template shop: update the instance or unlink it")).toBe(true);
    expect(isManagedByTemplate("name taken")).toBe(false);
  });
});

describe("instancesLabel", () => {
  it("counts instances and the ones behind", () => {
    expect(instancesLabel({ instances: 12, behind: 3 })).toBe("12 instances · 3 behind");
    expect(instancesLabel({ instances: 1, behind: 0 })).toBe("1 instance");
  });
});

describe("sealSecrets", () => {
  it("encrypts only typed secrets, with the key read at that moment", async () => {
    const api = fakeAPI({ "/api/v1/server/get-public-key": { public_key: "K1" } });
    const t = createTemplatesApi(ok);
    expect(await sealSecrets(t, "s1", { A: "x", B: "" })).toEqual({ A: "ct(K1):x" });
    expect(await sealSecrets(t, "s1", { B: "" })).toEqual({});
    expect(api.calls.map((c) => c.query)).toEqual(["?server_id=s1"]);
  });

  it("explains a server without a key yet", async () => {
    fakeAPI({ "/api/v1/server/get-public-key": new Error("public key unavailable for this server") });
    await expect(sealSecrets(createTemplatesApi(ok), "s1", { A: "x" })).rejects.toThrow(
      "This server has no encryption key yet",
    );
  });

  it("passes other failures through", async () => {
    fakeAPI({ "/api/v1/server/get-public-key": new ApiError("forbidden", 403) });
    await expect(sealSecrets(createTemplatesApi(ok), "s1", { A: "x" })).rejects.toThrow("forbidden");
  });
});
