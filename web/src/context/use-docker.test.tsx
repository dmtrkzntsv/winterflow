import { renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { useDocker } from "@/context/use-docker";

vi.mock("@/context/use-servers", () => ({ useServers: () => ({ activeServerId: "srv-1" }) }));
const waitFor = vi.fn();
vi.mock("@/context/use-notifications", () => ({ useNotifications: () => ({ waitFor }) }));
vi.mock("@/lib/ecies", () => ({
  encryptSecret: vi.fn(async (plain: string, key: string) => `enc(${plain}|${key})`),
}));

type Sent = { url: string; method: string; body: unknown };
let sent: Sent[] = [];
const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
  const url = String(input);
  sent.push({ url, method: init?.method ?? "GET", body: init?.body ? JSON.parse(String(init.body)) : undefined });
  if (url.includes("/api/v1/server/get-public-key")) {
    return new Response(JSON.stringify({ data: { public_key: "PUB" } }), { status: 200 });
  }
  return new Response(JSON.stringify({ data: { request_id: "r1" } }), { status: 202 });
});

describe("useDocker domain calls", () => {
  beforeEach(() => {
    sent = [];
    vi.stubGlobal("fetch", fetchMock);
    waitFor.mockResolvedValue({ status: 0, payload: {} });
  });
  afterEach(() => vi.unstubAllGlobals());

  it("encrypts the certificate key with the server's public key", async () => {
    const { result } = renderHook(() => useDocker());
    await result.current.createDomain("*.x.app", "certificate", { certificate: "CERT", key: "PLAIN-KEY" });

    const create = sent.find((s) => s.url.includes("/api/v1/domain/create"));
    expect(create?.url).toContain("server_id=srv-1");
    expect(create?.body).toEqual({ name: "*.x.app", tls: "certificate", certificate: "CERT", key: "enc(PLAIN-KEY|PUB)" });
    expect(JSON.stringify(sent)).not.toContain('"PLAIN-KEY"');
  });

  it("sends only the name and mode otherwise", async () => {
    const { result } = renderHook(() => useDocker());
    await result.current.createDomain("x.app", "off");
    expect(sent).toHaveLength(1);
    expect(sent[0].body).toEqual({ name: "x.app", tls: "off" });

    await result.current.deleteDomain("x.app");
    expect(sent[1]).toMatchObject({ method: "POST", body: { name: "x.app" } });
    expect(sent[1].url).toContain("/api/v1/domain/delete?server_id=srv-1");
  });

  it("resolves create and delete with the reload warnings", async () => {
    const { result } = renderHook(() => useDocker());
    waitFor.mockResolvedValueOnce({ status: 0, payload: { domain: { name: "x.app" }, warnings: ["reload failed"] } });
    expect(await result.current.createDomain("x.app", "off")).toEqual(["reload failed"]);
    waitFor.mockResolvedValueOnce({ status: 0, payload: { name: "x.app" } });
    expect(await result.current.deleteDomain("x.app")).toEqual([]);
    waitFor.mockResolvedValueOnce({ status: 0, payload: null });
    expect(await result.current.deleteDomain("x.app")).toEqual([]);
  });

  it("surfaces the API's refusal message", async () => {
    const { result } = renderHook(() => useDocker());
    fetchMock.mockResolvedValueOnce(
      new Response(JSON.stringify({ success: false, message: '"*.*.x" is not a valid domain' }), { status: 400 }),
    );
    await expect(result.current.createDomain("*.*.x", "off")).rejects.toThrow('"*.*.x" is not a valid domain');
    // A body without a message (or not JSON) falls back to the status.
    fetchMock.mockResolvedValueOnce(new Response("upstream down", { status: 502 }));
    await expect(result.current.deleteDomain("x.app")).rejects.toThrow("Request failed: 502");
    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify({ success: false, message: "  " }), { status: 403 }));
    await expect(result.current.listDomains()).rejects.toThrow("Request failed: 403");
  });

  it("lists domains from the result payload", async () => {
    const { result } = renderHook(() => useDocker());
    waitFor.mockResolvedValueOnce({ status: 0, payload: { domains: null } });
    expect(await result.current.listDomains()).toEqual([]);
    waitFor.mockResolvedValueOnce({ status: 0, payload: { domains: [{ name: "x.app", tls: "auto", apps: [] }] } });
    expect(await result.current.listDomains()).toEqual([{ name: "x.app", tls: "auto", apps: [] }]);
    waitFor.mockResolvedValueOnce({ status: 1, error: "ingress is disabled on this server" });
    await expect(result.current.listDomains()).rejects.toThrow("ingress is disabled on this server");
  });
});
