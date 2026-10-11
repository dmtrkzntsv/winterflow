import { describe, expect, it } from "vitest";

import { readApiError } from "@/lib/api";

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
