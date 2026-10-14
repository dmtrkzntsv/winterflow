import { renderHook, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { useRegistrations } from "@/hooks/use-registrations";
import { fakeAPI } from "@/test/fake-api";

// fakeAPI stubs fetch globally.
afterEach(() => vi.unstubAllGlobals());

describe("useRegistrations", () => {
  it("reads the server's domains", async () => {
    const api = fakeAPI({ "/api/v1/domain/get-domains": { domains: [{ name: "example.com", tls: "auto" }] } });
    const { result } = renderHook(() => useRegistrations("s1"));
    await waitFor(() => expect(result.current.registrations).toEqual([{ name: "example.com", tls: "auto" }]));
    expect(api.calls[0].query).toBe("?server_id=s1");
  });

  it("asks nothing while disabled (a server without the built-in proxy)", async () => {
    const api = fakeAPI({ "/api/v1/domain/get-domains": { domains: [] } });
    renderHook(() => useRegistrations("s1", false));
    await new Promise((r) => setTimeout(r, 20));
    expect(api.calls).toEqual([]);
  });
});
