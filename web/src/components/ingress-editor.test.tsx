import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { IngressEditor } from "@/components/ingress-editor";
import type { RegisteredDomain } from "@/lib/domains";
import type { AppEditorState, AppIngress } from "@/types/app-config";

const REGISTRATIONS: RegisteredDomain[] = [
  { name: "*.twillingate.app", tls: "certificate", apps: [] },
  { name: "example.com", tls: "auto", apps: [] },
  { name: "*.tunnel.example.com", tls: "off", apps: [] },
];

vi.mock("@/context/use-docker", () => ({
  useDocker: () => ({ listDomains: listDomainsMock }),
}));
const listDomainsMock = vi.fn(() => Promise.resolve(REGISTRATIONS));

// check answers GET /api/v1/domains/check per hostname.
type Check = { available: boolean; covered: boolean; claims: { app_name: string }[] | null };
let checks: Record<string, Check> = {};
const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
  const url = new URL(String(input), "http://localhost");
  const domain = url.searchParams.get("domain") ?? "";
  const data = checks[domain] ?? { available: true, covered: true, claims: null };
  return new Response(JSON.stringify({ success: true, data }), { status: 200 });
});

function Harness({ ingress }: { ingress: AppIngress }) {
  const [state, setState] = useState<AppEditorState>({
    config: { id: "app-1", name: "x", files: [], variables: [], ingress },
    files: {},
    variables: {},
  });
  return (
    <MemoryRouter>
      <IngressEditor state={state} onChange={setState} appId="app-1" serverId="srv-1" />
    </MemoryRouter>
  );
}

const oneDomain = (domain: string): AppIngress => ({
  domains: [{ id: "d1", domain, upstream_port: 8080, ssl: true }],
  redirects: [],
});

describe("IngressEditor", () => {
  beforeEach(() => {
    checks = {};
    fetchMock.mockClear();
    vi.stubGlobal("fetch", fetchMock);
  });
  afterEach(() => vi.unstubAllGlobals());

  it("suggests registrations; a wildcard pre-fills the suffix with the caret before it", async () => {
    const user = userEvent.setup();
    render(<Harness ingress={oneDomain("")} />);

    await user.click(await screen.findByRole("button", { name: "*.twillingate.app" }));
    const input = screen.getByLabelText<HTMLInputElement>("Hostname");
    expect(input).toHaveValue(".twillingate.app");
    await waitFor(() => expect(input).toHaveFocus());
    expect(input.selectionStart).toBe(0);

    await user.type(input, "shop", { skipClick: true });
    expect(input).toHaveValue("shop.twillingate.app");
    // The covering registration's mode replaces the old SSL switch.
    expect(screen.getByText("Certificate")).toBeInTheDocument();
    expect(screen.queryByRole("switch")).not.toBeInTheDocument();
  });

  it("fills an exact registration as is and checks it on this server", async () => {
    const user = userEvent.setup();
    render(<Harness ingress={oneDomain("")} />);

    await user.click(await screen.findByRole("button", { name: "example.com" }));
    expect(screen.getByLabelText("Hostname")).toHaveValue("example.com");
    expect(screen.getByText("Auto TLS")).toBeInTheDocument();
    await waitFor(() => expect(fetchMock).toHaveBeenCalled());
    const url = new URL(String(fetchMock.mock.calls[0][0]), "http://localhost");
    expect(url.searchParams.get("server_id")).toBe("srv-1");
    expect(url.searchParams.get("domain")).toBe("example.com");
    expect(url.searchParams.get("app_id")).toBe("app-1");
  });

  it("explains an uncovered hostname with a link to the Domains tab", async () => {
    const user = userEvent.setup();
    checks["shop.other.app"] = { available: true, covered: false, claims: null };
    render(<Harness ingress={oneDomain("")} />);

    await user.type(screen.getByLabelText("Hostname"), "shop.other.app");
    expect(await screen.findByText(/Not covered by a domain registered on this server/)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Register a domain" })).toHaveAttribute("href", "/settings?tab=domains");
  });

  it("names the app that already takes the hostname", async () => {
    const user = userEvent.setup();
    checks["shop.twillingate.app"] = { available: false, covered: true, claims: [{ app_name: "Shop" }] };
    render(<Harness ingress={oneDomain("")} />);

    await user.type(screen.getByLabelText("Hostname"), "shop.twillingate.app");
    expect(await screen.findByText('Taken by app "Shop" on this server.')).toBeInTheDocument();
  });

  it("checks domain-level redirect sources and shows their mode", async () => {
    const user = userEvent.setup();
    checks["old.other.app"] = { available: true, covered: false, claims: null };
    render(
      <Harness
        ingress={{
          domains: [],
          redirects: [
            { id: "r1", domain: "", path: "", to: "https://example.com", code: 301, ssl: true },
            { id: "r2", domain: "x.tunnel.example.com", path: "", to: "https://example.com", code: 301, ssl: true },
          ],
        }}
      />,
    );
    expect(await screen.findByText("TLS off")).toBeInTheDocument();
    const [first] = screen.getAllByLabelText("Redirect hostname");
    await user.type(first, "old.other.app");
    expect(await screen.findByText(/Not covered/)).toBeInTheDocument();
  });

  it("works without registrations", async () => {
    listDomainsMock.mockRejectedValueOnce(new Error("agent offline"));
    render(<Harness ingress={oneDomain("")} />);
    await waitFor(() => expect(listDomainsMock).toHaveBeenCalled());
    expect(screen.queryByText("Use:")).not.toBeInTheDocument();
  });
});
