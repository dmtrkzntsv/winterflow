import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import { AppGrid } from "@/components/app-grid";
import { AppsContext, type App, type AppsContextValue } from "@/context/apps-context-base";
import { fakeAPI } from "@/test/fake-api";
import type { InstanceView, TemplateSummary } from "@/lib/templates";

let listedTemplates: TemplateSummary[] = [];
let listedInstances: InstanceView[] = [];
afterEach(() => {
  vi.unstubAllGlobals();
  listedTemplates = [];
  listedInstances = [];
});

// Links point into the active server's scope (/server/:id/...).
let activeServerId = "srv-1";
// srv-1 runs the built-in proxy: its registered domains decide HTTPS.
const servers = [{ id: "srv-1", features: { ingress: true } }];
const templatesApi = { listTemplates: async () => listedTemplates, listInstances: async () => listedInstances };
vi.mock("@/context/use-servers", () => ({ useServers: () => ({ activeServerId, servers }) }));
vi.mock("@/hooks/use-templates-api", () => ({ useTemplatesApi: () => templatesApi }));

const app = (id: string, name: string, over: Partial<App> = {}): App => ({
  id,
  serverId: "srv-1",
  name,
  version: "1",
  icon: "",
  color: "",
  createdAt: "",
  ...over,
});

const tree = (over: Partial<AppsContextValue>) => (
  <MemoryRouter>
    <AppsContext.Provider
      value={
        {
          apps: [app("a1", "Shop"), app("a2", "Blog")],
          statusByApp: { a1: 1, a2: 5 },
          backupsByApp: {},
          staleVarsByApp: {},
          loading: false,
          error: null,
          ...over,
        } as unknown as AppsContextValue
      }
    >
      <AppGrid />
    </AppsContext.Provider>
  </MemoryRouter>
);

function renderGrid(over: Partial<AppsContextValue>) {
  return render(tree(over));
}

const card = (name: string) => screen.getByRole("link", { name }).closest("li")!;

describe("AppGrid", () => {
  it("shows a catalog instance upgrade state even without an address or warning", async () => {
    listedTemplates = [{
      id: "t1", source: "catalog", slug: "proxy", name: "Proxy", latest_revision: 4, instances: 1, behind: 1,
      withdrawn_at: null, updated_at: "", catalog: { summary: "", description: "", keywords: [], tags: [], alternative_to: [], license: "", homepage_url: "", repository_url: "", documentation_url: "", logo_url: "https://winterflow.io/catalog/logos/proxy.svg" },
    }];
    listedInstances = [{ key: "k1", server_id: "srv-1", app_id: "a1", template_id: "t1", template_name: "Proxy", revision: 2, name: "Shop", values: {}, secrets: {}, hosts: [], state: "created", status_code: 1, last_error: "", created_at: "", updated_at: "" }];
    renderGrid({ apps: [app("a1", "Shop", { templateId: "t1" })] });
    expect(await within(card("Shop")).findByText("Upgrade available")).toBeInTheDocument();
    expect(within(card("Shop")).getByRole("presentation")).toHaveAttribute("src", "https://winterflow.io/catalog/logos/proxy.svg");
  });
  it("opens an address under a registered domain by https, whatever the route's own flag says", async () => {
    const api = fakeAPI({
      "/api/v1/domain/get-domains": { domains: [{ name: "*.tunnel.example", tls: "off" }] },
    });
    renderGrid({
      apps: [
        app("a1", "Shop", { domains: [{ domain: "shop.tunnel.example", ssl: false, kind: "route" }] }),
        app("a2", "Blog", { domains: [{ domain: "blog.elsewhere.example", ssl: false, kind: "route" }] }),
      ],
    });
    await waitFor(() =>
      expect(within(card("Shop")).getByRole("link", { name: "shop.tunnel.example" })).toHaveAttribute("href", "https://shop.tunnel.example"),
    );
    // Not covered by a registration: the route's own flag, as before.
    expect(within(card("Blog")).getByRole("link", { name: "blog.elsewhere.example" })).toHaveAttribute(
      "href",
      "http://blog.elsewhere.example",
    );
    expect(api.calls.filter((c) => c.path === "/api/v1/domain/get-domains")).toHaveLength(1);
  });

  it("shows each app's status and where it lives, never its id", () => {
    renderGrid({
      apps: [
        app("a1", "Shop", { domains: [{ domain: "shop.example.com", ssl: true, kind: "route" }] }),
        app("a2", "Blog"),
        app("0123456789", ""),
      ],
      statusByApp: { a1: 1, a2: 5 },
    });
    const shop = within(card("Shop"));
    expect(shop.getByText("Running")).toBeInTheDocument();
    expect(shop.getByRole("link", { name: "shop.example.com" })).toHaveAttribute("href", "https://shop.example.com");
    // On a phone the address is a 40px tap target (16px line + 12px above and below).
    expect(shop.getByRole("link", { name: "shop.example.com" })).toHaveClass("max-sm:py-3", "max-sm:-my-3");
    expect(within(card("Blog")).getByText("Stopped")).toBeInTheDocument();
    // No domain: no address line at all.
    expect(card("Blog")).not.toHaveTextContent("No public address");
    expect(within(card("Blog")).getAllByRole("link")).toHaveLength(1);
    expect(screen.getByRole("link", { name: "Unnamed app" })).toBeInTheDocument();
    expect(screen.queryByText(/01234567/)).not.toBeInTheDocument();
  });

  it("reads an app whose deploy failed as Failed", () => {
    renderGrid({ apps: [app("a1", "Shop"), app("a2", "Blog")], statusByApp: { a1: 2, a2: 1 }, deployFailures: { a1: "boom" } });
    expect(within(card("Shop")).getByText("Failed")).toBeInTheDocument();
    expect(within(card("Blog")).getByText("Running")).toBeInTheDocument();
  });

  it("carries one warning chip, for a late or failing backup or shared variables to reload", () => {
    renderGrid({
      backupsByApp: { a1: [{ name: "db", state: "failing", last_error: "disk full" }], a2: [{ name: "db", state: "ok" }] },
      staleVarsByApp: { a2: ["SMTP_PASS"] },
      statusByApp: { a1: 1, a2: 1 },
    });
    expect(within(card("Shop")).getByText("Backup failing")).toBeInTheDocument();
    expect(within(card("Blog")).getByText("Reload needed")).toBeInTheDocument();
  });

  it("links a backup warning to the app's Backups tab, and the others to the app", () => {
    renderGrid({ backupsByApp: { a1: [{ name: "db", state: "late" }], a2: [{ name: "db", state: "ok" }] } });
    expect(screen.getByRole("link", { name: "Shop" })).toHaveAttribute("href", "/server/srv-1/app/a1?tab=backups");
    expect(screen.getByRole("link", { name: "Blog" })).toHaveAttribute("href", "/server/srv-1/app/a2");
  });

  it("warns about shared variables only for an app that runs", () => {
    renderGrid({ staleVarsByApp: { a1: ["SMTP_PASS"], a2: ["SMTP_PASS"] } });
    expect(within(card("Shop")).getByText("Reload needed")).toBeInTheDocument();
    expect(within(card("Blog")).queryByText("Reload needed")).not.toBeInTheDocument();
  });

  it("shows no chip while all is well", () => {
    renderGrid({});
    expect(screen.queryByText(/Backup (late|failing)|Reload needed/)).not.toBeInTheDocument();
  });

  it("offers a filter only past eight apps, matching names and addresses", async () => {
    const user = userEvent.setup();
    const many = Array.from({ length: 9 }, (_, i) => app(`a${i}`, `App ${i}`));
    many[3] = app("a3", "Photos", { domains: [{ domain: "pics.lan", ssl: false, kind: "route" }] });
    const { unmount } = renderGrid({ apps: many.slice(0, 8) });
    expect(screen.queryByRole("searchbox", { name: "Filter apps" })).not.toBeInTheDocument();
    unmount();
    renderGrid({ apps: many });
    // The cards come in quickly: the stagger stops after the sixth (100ms).
    const delays = within(screen.getByRole("list", { name: "Apps" }))
      .getAllByRole("listitem")
      .map((li) => li.style.animationDelay);
    expect(delays.slice(0, 3)).toEqual(["0ms", "20ms", "40ms"]);
    expect(delays.at(-1)).toBe("100ms");
    await user.type(screen.getByRole("searchbox", { name: "Filter apps" }), "pics");
    expect(within(screen.getByRole("list", { name: "Apps" })).getAllByRole("listitem")).toHaveLength(1);
    expect(screen.getByRole("link", { name: "Photos" })).toBeInTheDocument();
    await user.clear(screen.getByRole("searchbox", { name: "Filter apps" }));
    await user.type(screen.getByRole("searchbox", { name: "Filter apps" }), "nothing like it");
    expect(screen.getByText("No app matches “nothing like it”.")).toBeInTheDocument();
  });

  it("does not filter by a query the hidden box can no longer clear", async () => {
    const user = userEvent.setup();
    const many = Array.from({ length: 9 }, (_, i) => app(`a${i}`, `App ${i}`));
    const { rerender } = renderGrid({ apps: many });
    await user.type(screen.getByRole("searchbox", { name: "Filter apps" }), "App 7");
    expect(within(screen.getByRole("list", { name: "Apps" })).getAllByRole("listitem")).toHaveLength(1);
    // An app was deleted (or another server shown): three apps, no box.
    rerender(tree({ apps: many.slice(0, 3) }));
    expect(screen.queryByRole("searchbox", { name: "Filter apps" })).not.toBeInTheDocument();
    expect(within(screen.getByRole("list", { name: "Apps" })).getAllByRole("listitem")).toHaveLength(3);
  });

  it("forgets the query when the active server changes", async () => {
    const user = userEvent.setup();
    const many = Array.from({ length: 9 }, (_, i) => app(`a${i}`, `App ${i}`));
    const { rerender } = renderGrid({ apps: many });
    await user.type(screen.getByRole("searchbox", { name: "Filter apps" }), "App 7");
    activeServerId = "srv-2";
    rerender(tree({ apps: many }));
    expect(screen.getByRole("searchbox", { name: "Filter apps" })).toHaveValue("");
    expect(within(screen.getByRole("list", { name: "Apps" })).getAllByRole("listitem")).toHaveLength(9);
    activeServerId = "srv-1";
  });

  it("invites to run the first app from a template or a compose file", () => {
    renderGrid({ apps: [] });
    expect(screen.getByText("Run your first app")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /From a template/ })).toHaveAttribute("href", "/server/srv-1/new?from=template");
    expect(screen.getByRole("link", { name: /Compose file/ })).toHaveAttribute("href", "/server/srv-1/new?from=compose");
    // The page header's New app is the one filled button: neither start is.
    for (const name of [/From a template/, /Compose file/]) {
      expect(screen.getByRole("link", { name })).toHaveClass("border");
      expect(screen.getByRole("link", { name })).not.toHaveClass("bg-primary");
    }
  });

  it("shows skeletons while the first list loads, and a read failure", () => {
    const { unmount } = renderGrid({ apps: [], loading: true });
    expect(screen.getAllByRole("status", { name: "Loading apps" })).toHaveLength(3);
    unmount();
    renderGrid({ error: "agent offline" });
    expect(screen.getByText("agent offline")).toBeInTheDocument();
  });
});
