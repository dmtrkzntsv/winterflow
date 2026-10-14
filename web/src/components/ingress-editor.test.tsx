import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { AddressRows, RedirectRows } from "@/components/ingress-editor";
import { useRegistrations } from "@/hooks/use-registrations";
import type { RegisteredDomain } from "@/lib/domains";
import type { FieldErrors } from "@/lib/field-errors";
import type { AppEditorState, AppIngress } from "@/types/app-config";

// Links point into the active server's scope (/server/:id/...).
vi.mock("@/context/use-servers", () => ({ useServers: () => ({ activeServerId: "srv-1" }) }));

const WILDCARD: RegisteredDomain = { name: "*.twillingate.app", tls: "certificate" };
const EXACT: RegisteredDomain = { name: "example.com", tls: "auto" };
const TUNNEL: RegisteredDomain = { name: "*.tunnel.example.com", tls: "off" };

// The fake API: GET get-domains answers `registrations` (null = a 500);
// GET domains/check answers checks[domain], else the covering registration
// from `covering` (or fails, when offline).
type Check = {
  available: boolean;
  covered: boolean;
  registration?: RegisteredDomain;
  claims: { app_name: string }[] | null;
};
let registrations: RegisteredDomain[] | null;
let checks: Record<string, Check>;
let offline: boolean;
const covering: Record<string, RegisteredDomain> = {
  "shop.twillingate.app": WILDCARD,
  "example.com": EXACT,
  "x.tunnel.example.com": TUNNEL,
  "y.tunnel.example.com": TUNNEL,
};
const ok = (data: unknown) => new Response(JSON.stringify({ success: true, data }), { status: 200 });
const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
  const url = new URL(String(input), "http://localhost");
  if (url.pathname === "/api/v1/domain/get-domains") {
    return registrations === null
      ? new Response(JSON.stringify({ success: false, message: "boom" }), { status: 500 })
      : ok({ domains: registrations });
  }
  if (offline) throw new Error("offline");
  const domain = url.searchParams.get("domain") ?? "";
  const reg = covering[domain];
  return ok(checks[domain] ?? { available: true, covered: reg !== undefined, registration: reg, claims: null });
});
const calls = (path: string) =>
  fetchMock.mock.calls
    .map(([input]) => new URL(String(input), "http://localhost"))
    .filter((url) => url.pathname === path);

function Harness({ ingress, errors }: { ingress: AppIngress; errors?: FieldErrors }) {
  const [state, setState] = useState<AppEditorState>({
    config: { id: "app-1", name: "x", files: [], variables: [], ingress },
    files: {},
    variables: {},
    storageVariables: [],
  });
  const { registrations, refresh } = useRegistrations("srv-1");
  const ing = state.config.ingress;
  const rows = { state, onChange: setState, appId: "app-1", serverId: "srv-1", registrations, onRefresh: refresh, errors };
  return (
    <MemoryRouter>
      <AddressRows {...rows} />
      <RedirectRows {...rows} />
      <output data-testid="ingress">{JSON.stringify(ing)}</output>
      <output data-testid="ssl">
        {JSON.stringify([...(ing?.domains ?? []), ...(ing?.redirects ?? [])].map((r) => [r.domain, r.ssl]))}
      </output>
    </MemoryRouter>
  );
}
const ssl = () => JSON.parse(screen.getByTestId("ssl").textContent ?? "[]") as [string, boolean][];

const oneDomain = (domain: string): AppIngress => ({
  domains: [{ id: "d1", domain, upstream_port: 8080, ssl: true }],
  redirects: [],
});

describe("ingress rows", () => {
  beforeEach(() => {
    registrations = [WILDCARD, EXACT, TUNNEL];
    checks = {};
    offline = false;
    fetchMock.mockClear();
    vi.stubGlobal("fetch", fetchMock);
  });
  afterEach(() => vi.unstubAllGlobals());

  it("suggests registrations; a wildcard pre-fills the suffix with the caret before it", async () => {
    const user = userEvent.setup({ delay: null });
    render(<Harness ingress={oneDomain("")} />);

    await user.click(await screen.findByRole("button", { name: "*.twillingate.app" }));
    const input = screen.getByLabelText<HTMLInputElement>("Hostname");
    expect(input).toHaveValue(".twillingate.app");
    await waitFor(() => expect(input).toHaveFocus());
    expect(input.selectionStart).toBe(0);

    await user.type(input, "shop", { skipClick: true });
    expect(input).toHaveValue("shop.twillingate.app");
    // The covering registration's mode replaces the old SSL switch.
    expect(await screen.findByText("Certificate")).toBeInTheDocument();
    expect(screen.getByText("*.twillingate.app", { selector: "code" })).toBeInTheDocument();
    expect(screen.queryByRole("switch")).not.toBeInTheDocument();
    // The half-typed ".twillingate.app" never went to the API.
    expect(calls("/api/v1/domains/check").map((u) => u.searchParams.get("domain"))).toEqual([
      "shop.twillingate.app",
    ]);
  });

  it("fills an exact registration as is and checks it on this server", async () => {
    const user = userEvent.setup({ delay: null });
    render(<Harness ingress={oneDomain("")} />);

    await user.click(await screen.findByRole("button", { name: "example.com" }));
    expect(screen.getByLabelText("Hostname")).toHaveValue("example.com");
    expect(await screen.findByText("Auto TLS")).toBeInTheDocument();
    const [url] = calls("/api/v1/domains/check");
    expect(url.searchParams.get("server_id")).toBe("srv-1");
    expect(url.searchParams.get("domain")).toBe("example.com");
    expect(url.searchParams.get("app_id")).toBe("app-1");
    expect(calls("/api/v1/domain/get-domains")[0].searchParams.get("server_id")).toBe("srv-1");
  });

  it("stores the legacy ssl flag from an edited hostname's registration", async () => {
    const user = userEvent.setup({ delay: null });
    render(<Harness ingress={oneDomain("")} />);

    await user.type(screen.getByLabelText("Hostname"), "y.tunnel.example.com");
    expect(await screen.findByText("TLS off")).toBeInTheDocument();
    await waitFor(() => expect(ssl()).toEqual([["y.tunnel.example.com", false]]));

    await user.clear(screen.getByLabelText("Hostname"));
    await user.type(screen.getByLabelText("Hostname"), "example.com");
    await waitFor(() => expect(ssl()).toEqual([["example.com", true]]));
  });

  it("explains an uncovered hostname with a link to the Domains tab", async () => {
    const user = userEvent.setup({ delay: null });
    render(<Harness ingress={oneDomain("")} />);

    await user.type(screen.getByLabelText("Hostname"), "shop.other.app");
    const hint = await screen.findByText(/No domain on this server covers this hostname/);
    expect(screen.getByLabelText("Hostname")).toHaveAttribute("aria-invalid", "true");
    expect(screen.getByLabelText("Hostname")).toHaveAttribute("aria-describedby", hint.id);
    // A new tab, so following it doesn't lose the unsaved form.
    const link = screen.getByRole("link", { name: /Add it/ });
    expect(link).toHaveAttribute("href", "/server/srv-1/settings/domains");
    expect(link).toHaveAttribute("target", "_blank");
    // Nothing covers it: the stored flag keeps its default.
    expect(ssl()).toEqual([["shop.other.app", true]]);

    // Removing the row drops it and its hint.
    await user.click(screen.getByRole("button", { name: "Remove shop.other.app" }));
    expect(screen.queryByLabelText("Hostname")).not.toBeInTheDocument();
    expect(screen.queryByText(/No domain on this server covers/)).not.toBeInTheDocument();
    expect(screen.getByText(/Add a hostname to send web traffic/)).toBeInTheDocument();
  });

  it("re-checks an uncovered hostname when the user comes back to the tab", async () => {
    const user = userEvent.setup({ delay: null });
    render(<Harness ingress={oneDomain("")} />);

    await user.type(screen.getByLabelText("Hostname"), "shop.other.app");
    // An edited hostname is checked after a 400 ms debounce, which leaves the
    // default 1 s wait little room on a loaded CI runner: wait longer.
    await screen.findByText(/No domain on this server covers this hostname/, undefined, { timeout: 4000 });
    const loads = calls("/api/v1/domain/get-domains").length;

    // Registered in the other tab meanwhile.
    const other: RegisteredDomain = { name: "*.other.app", tls: "auto" };
    registrations = [...(registrations ?? []), other];
    covering["shop.other.app"] = other;
    act(() => {
      window.dispatchEvent(new Event("focus"));
    });

    await waitFor(() => expect(screen.queryByText(/No domain on this server covers/)).not.toBeInTheDocument(), {
      timeout: 4000,
    });
    expect(calls("/api/v1/domain/get-domains").length).toBe(loads + 1);
    expect(await screen.findByText("Auto TLS", undefined, { timeout: 4000 })).toBeInTheDocument();
    expect(screen.getByText("*.other.app")).toBeInTheDocument();
    expect(screen.getByLabelText("Hostname")).toHaveAttribute("aria-invalid", "false");

    // Covered now: coming back again costs nothing.
    const after = fetchMock.mock.calls.length;
    act(() => {
      window.dispatchEvent(new Event("focus"));
    });
    expect(fetchMock.mock.calls.length).toBe(after);
    delete covering["shop.other.app"];
  });

  it("re-checks when the tab is refocused the moment the hint appears", async () => {
    const user = userEvent.setup({ delay: null });
    render(<Harness ingress={oneDomain("")} />);

    await user.type(screen.getByLabelText("Hostname"), "shop.other.app");
    try {
      // Focus lands in the microtask right after the commit that shows the
      // hint, before React has flushed that commit's passive effects: the
      // return must not be lost to a listener that is not attached yet.
      const watcher = new MutationObserver(() => {
        if (!screen.queryByText(/No domain on this server covers/)) return;
        watcher.disconnect();
        // Registered in the other tab meanwhile.
        covering["shop.other.app"] = { name: "*.other.app", tls: "auto" };
        window.dispatchEvent(new Event("focus"));
      });
      watcher.observe(document.body, { childList: true, subtree: true });

      await screen.findByText(/No domain on this server covers this hostname/, undefined, { timeout: 4000 });
      expect(await screen.findByText("Auto TLS", undefined, { timeout: 4000 })).toBeInTheDocument();
      expect(screen.queryByText(/No domain on this server covers/)).not.toBeInTheDocument();
    } finally {
      delete covering["shop.other.app"];
    }
  });

  it("names the app that already takes the hostname", async () => {
    const user = userEvent.setup({ delay: null });
    checks["shop.twillingate.app"] = {
      available: false,
      covered: true,
      registration: WILDCARD,
      claims: [{ app_name: "Shop" }],
    };
    render(<Harness ingress={oneDomain("")} />);

    await user.type(screen.getByLabelText("Hostname"), "shop.twillingate.app");
    expect(await screen.findByText(/Already used by/)).toHaveTextContent("Already used by Shop on this server.");
  });

  it("checks domain-level redirect sources, not path rules", async () => {
    const user = userEvent.setup({ delay: null });
    render(
      <Harness
        ingress={{
          domains: [],
          redirects: [
            { id: "r1", domain: "", path: "", to: "https://example.com", code: 301, ssl: true },
            { id: "r2", domain: "x.tunnel.example.com", path: "", to: "https://example.com", code: 301, ssl: true },
            { id: "r3", domain: "y.tunnel.example.com", path: "/old/*", to: "https://example.com", code: 301, ssl: true },
          ],
        }}
      />,
    );
    // A loaded hostname shows its mode at once, keeping its stored flag.
    expect(await screen.findByText("TLS off")).toBeInTheDocument();
    expect(ssl()).toContainEqual(["x.tunnel.example.com", true]);
    expect(calls("/api/v1/domains/check").map((u) => u.searchParams.get("domain"))).toEqual([
      "x.tunnel.example.com",
    ]);

    const [first] = screen.getAllByLabelText("Redirect hostname");
    await user.type(first, "old.other.app");
    expect(await screen.findByText(/No domain on this server covers/)).toBeInTheDocument();

    // Removing the redirect drops its hint too.
    await user.click(screen.getAllByRole("button", { name: "Remove redirect" })[0]);
    expect(screen.getAllByLabelText("Redirect hostname")).toHaveLength(2);
    expect(screen.queryByText(/No domain on this server covers/)).not.toBeInTheDocument();
  });

  it("adds and edits hostnames and redirects", async () => {
    const user = userEvent.setup({ delay: null });
    render(<Harness ingress={{ domains: [], redirects: [] }} />);
    const ingress = () => JSON.parse(screen.getByTestId("ingress").textContent ?? "{}") as AppIngress;

    await user.click(screen.getByRole("button", { name: /Add hostname/ }));
    await user.type(screen.getByLabelText("Hostname"), "example.com");
    await user.type(screen.getByLabelText("Port"), "8088");
    await user.click(screen.getByRole("button", { name: /Add redirect/ }));
    await user.type(screen.getByLabelText("Redirect hostname"), "x.tunnel.example.com");
    await user.type(screen.getByLabelText("Redirect to"), "https://example.com");
    await user.type(screen.getByLabelText("Redirect path"), "/old");
    await user.clear(screen.getByLabelText("Redirect path"));

    // The redirect's flag follows its registration (TLS off) too.
    await waitFor(() => expect(ingress().redirects[0]).toMatchObject({ ssl: false }));
    expect(ingress().domains).toMatchObject([{ domain: "example.com", upstream_port: 8088 }]);
    expect(ingress().redirects).toMatchObject([
      { domain: "x.tunnel.example.com", path: "", to: "https://example.com", code: 301 },
    ]);
    await user.clear(screen.getByLabelText("Port"));
    expect(ingress().domains[0].upstream_port).toBe("");
  });

  it("works without registrations", async () => {
    registrations = null;
    offline = true;
    const user = userEvent.setup({ delay: null });
    render(<Harness ingress={oneDomain("")} />);
    await waitFor(() => expect(calls("/api/v1/domain/get-domains")).toHaveLength(1));
    expect(screen.queryByText("Start from:")).not.toBeInTheDocument();
    // The hostname field still works, just without suggestions or a badge.
    await user.type(screen.getByLabelText("Hostname"), "shop.example.com");
    await waitFor(() => expect(calls("/api/v1/domains/check")).toHaveLength(1));
    expect(screen.getByLabelText("Hostname")).toHaveValue("shop.example.com");
    expect(screen.queryByText("Auto TLS")).not.toBeInTheDocument();
    expect(screen.getByLabelText("Hostname")).not.toHaveAttribute("aria-describedby");
  });
});

describe("ingress rows: messages", () => {
  beforeEach(() => {
    registrations = [WILDCARD, EXACT, TUNNEL];
    checks = {};
    offline = false;
    vi.stubGlobal("fetch", fetchMock);
  });
  afterEach(() => vi.unstubAllGlobals());

  it("shows a hostname's and a port's message under their row", () => {
    render(
      <Harness
        ingress={oneDomain("Bad Host")}
        errors={{ "domain:d1": "Use a lowercase hostname like app.example.com.", "port:d1": "Enter a port from 1 to 65535." }}
      />,
    );
    const host = screen.getByLabelText("Hostname");
    expect(host).toHaveAttribute("aria-invalid", "true");
    expect(host).toHaveAccessibleDescription("Use a lowercase hostname like app.example.com.");
    expect(screen.getByLabelText("Port")).toHaveAccessibleDescription("Enter a port from 1 to 65535.");
  });

  it("shows a redirect hostname's message under its row", () => {
    render(
      <Harness
        ingress={{
          domains: [],
          redirects: [{ id: "r1", domain: "late.example.com", path: "", to: "https://x.example.com", code: 301, ssl: true }],
        }}
        errors={{ "redirect-host:r1": "Add late.example.com as an address or a whole-domain redirect above this rule first." }}
      />,
    );
    const host = screen.getByLabelText("Redirect hostname");
    expect(host).toHaveAttribute("aria-invalid", "true");
    expect(host).toHaveAccessibleDescription(
      "Add late.example.com as an address or a whole-domain redirect above this rule first.",
    );
    expect(host).toHaveClass("border-destructive");
  });

  it("shows a redirect's messages under its fields", () => {
    render(
      <Harness
        ingress={{
          domains: [],
          redirects: [{ id: "r1", domain: "x.tunnel.example.com", path: "old", to: "nope", code: 301, ssl: true }],
        }}
        errors={{
          "redirect-path:r1": "The path must start with /.",
          "redirect-to:r1": "Send it to a full address starting with http:// or https://.",
        }}
      />,
    );
    expect(screen.getByLabelText("Redirect path")).toHaveAccessibleDescription("The path must start with /.");
    expect(screen.getByLabelText("Redirect to")).toHaveAccessibleDescription(
      "Send it to a full address starting with http:// or https://.",
    );
  });
});
