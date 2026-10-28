import { describe, expect, it, vi } from "vitest";

import { ApiError, apiFetch, apiInit, createJsonClient, jsonPost, readApiError } from "@/lib/api";

describe("readApiError", () => {
  it("uses the API's message", async () => {
    const res = new Response(JSON.stringify({ success: false, message: " name taken " }), { status: 400 });
    expect((await readApiError(res)).message).toBe("name taken");
  });

  it("falls back to the status without a usable message", async () => {
    expect((await readApiError(new Response("upstream down", { status: 502 }))).message).toBe("Request failed: 502");
    expect((await readApiError(new Response(JSON.stringify({ message: "  " }), { status: 403 }))).message).toBe(
      "Request failed: 403",
    );
    expect((await readApiError(new Response(JSON.stringify({ message: 7 }), { status: 500 }))).message).toBe(
      "Request failed: 500",
    );
  });
});

describe("createJsonClient", () => {
  it("sends the cookie and returns the envelope's data", async () => {
    const fetchImpl = vi.fn(async () => new Response(JSON.stringify({ success: true, data: { n: 1 } })));
    const { call } = createJsonClient("http://api", fetchImpl as unknown as typeof fetch);
    await expect(call<{ n: number }>("/x", jsonPost({ a: 1 }))).resolves.toEqual({ n: 1 });
    const [url, init] = fetchImpl.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe("http://api/x");
    expect(init).toMatchObject({ credentials: "include", method: "POST", body: '{"a":1}' });
    const headers = new Headers(init.headers);
    expect(headers.get("Content-Type")).toBe("application/json");
    expect(headers.get("X-Winterflow-Request")).toBe("1");
  });

  it("throws a refusal as an ApiError with its status", async () => {
    const fetchImpl = async () => new Response(JSON.stringify({ message: "taken" }), { status: 409 });
    const { request } = createJsonClient("", fetchImpl as typeof fetch);
    const err = await request("/x").catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect(err).toMatchObject({ message: "taken", status: 409 });
  });
});

describe("apiInit", () => {
  it("marks every write as the app's own and sends the cookie", () => {
    for (const method of ["POST", "put", "DELETE", "PATCH"]) {
      const init = apiInit({ method, headers: { "Content-Type": "application/json" } });
      expect(init.credentials).toBe("include");
      const headers = new Headers(init.headers);
      expect(headers.get("X-Winterflow-Request")).toBe("1");
      expect(headers.get("Content-Type")).toBe("application/json");
    }
  });

  it("leaves reads alone (they need no header; a header would cost a preflight)", () => {
    for (const init of [undefined, { method: "GET" }, { method: "HEAD" }]) {
      const out = apiInit(init);
      expect(out.credentials).toBe("include");
      expect(new Headers(out.headers).has("X-Winterflow-Request")).toBe(false);
    }
  });

  it("keeps a caller's credentials choice and Headers instance values", () => {
    const init = apiInit({ method: "POST", credentials: "omit", headers: new Headers({ A: "b" }) });
    expect(init.credentials).toBe("omit");
    expect(new Headers(init.headers).get("A")).toBe("b");
  });
});

describe("apiFetch", () => {
  it("sends the app-request header on a POST through the global fetch", async () => {
    const f = vi.fn(async () => new Response(null, { status: 204 }));
    vi.stubGlobal("fetch", f);
    try {
      await apiFetch("/api/v1/auth/logout", { method: "POST" });
      const init = (f.mock.calls[0] as unknown as [string, RequestInit])[1];
      expect(new Headers(init.headers).get("X-Winterflow-Request")).toBe("1");
    } finally {
      vi.unstubAllGlobals();
    }
  });
});
