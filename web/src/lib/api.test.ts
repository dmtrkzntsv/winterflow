import { describe, expect, it, vi } from "vitest";

import { ApiError, createJsonClient, jsonPost, readApiError } from "@/lib/api";

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
    expect(fetchImpl).toHaveBeenCalledWith("http://api/x", {
      credentials: "include",
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: '{"a":1}',
    });
  });

  it("throws a refusal as an ApiError with its status", async () => {
    const fetchImpl = async () => new Response(JSON.stringify({ message: "taken" }), { status: 409 });
    const { request } = createJsonClient("", fetchImpl as typeof fetch);
    const err = await request("/x").catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect(err).toMatchObject({ message: "taken", status: 409 });
  });
});
