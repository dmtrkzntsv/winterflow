import { renderHook, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { useAddressRegistrations, useRegistrations } from "@/hooks/use-registrations";
import { fakeAPI } from "@/test/fake-api";

let servers: { id: string; features?: Record<string, boolean> }[] = [];
vi.mock("@/context/use-servers", () => ({ useServers: () => ({ servers }) }));

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

  it("never shows another server's list, nor one once disabled", async () => {
    fakeAPI({ "/api/v1/domain/get-domains": (_b: unknown, q: URLSearchParams) => ({ domains: [{ name: `${q.get("server_id")}.example`, tls: "auto" }] }) });
    const { result, rerender } = renderHook(({ id, on }) => useRegistrations(id, on), { initialProps: { id: "s1", on: true } });
    await waitFor(() => expect(result.current.registrations).toEqual([{ name: "s1.example", tls: "auto" }]));
    rerender({ id: "s2", on: true });
    expect(result.current.registrations).toEqual([]);
    await waitFor(() => expect(result.current.registrations).toEqual([{ name: "s2.example", tls: "auto" }]));
    rerender({ id: "s2", on: false });
    expect(result.current.registrations).toEqual([]);
  });

  it("reads an address's server's domains only when it runs the built-in proxy", async () => {
    const api = fakeAPI({ "/api/v1/domain/get-domains": { domains: [{ name: "example.com", tls: "off" }] } });
    servers = [{ id: "s1", features: { ingress: true } }, { id: "s2" }];
    const { result, rerender } = renderHook(({ id }) => useAddressRegistrations(id), { initialProps: { id: "s1" as string | null } });
    await waitFor(() => expect(result.current).toEqual([{ name: "example.com", tls: "off" }]));
    rerender({ id: "s2" });
    expect(result.current).toEqual([]);
    rerender({ id: null });
    expect(result.current).toEqual([]);
    expect(api.calls).toHaveLength(1);
  });
});
