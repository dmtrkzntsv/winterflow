import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { TemplateFlow } from "@/components/new-app/template-flow";
import { NotificationsContext, type NotificationsContextValue } from "@/context/notifications-context-base";
import { ApiError, LOAD_TIMEOUT_MS, type TemplateView } from "@/lib/templates";
import { fakeAPI } from "@/test/fake-api";
import type { DeclaredVariable } from "@/types/app-config";

const toast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn() }));
vi.mock("sonner", () => ({ toast }));
vi.mock("@/lib/ecies", () => ({
  ENCRYPTED: "<encrypted>",
  encryptSecret: vi.fn(async (value: string, key: string) => `ct(${key}):${value}`),
}));
// ingress says whether server s1 runs the built-in proxy (hostnames).
const ingress = vi.hoisted(() => ({ on: false }));
vi.mock("@/context/use-servers", () => ({
  useServers: () => ({ activeServerId: "s1", servers: [{ id: "s1", name: "alpha", features: { ingress: ingress.on } }] }),
}));
const refresh = vi.hoisted(() => vi.fn(async () => {}));
const reconcile = vi.hoisted(() => vi.fn(async (): Promise<{ id: string }[]> => []));
const noteDeployFailure = vi.hoisted(() => vi.fn());
const apps = vi.hoisted(() => ({ list: [] as { id: string; name: string; serverId: string }[] }));
vi.mock("@/context/use-apps", () => ({
  useApps: () => ({ apps: apps.list, refresh, reconcile, noteDeployFailure }),
}));
const serverStorages = vi.hoisted(() => vi.fn(async () => ["tenants", "local"]));
vi.mock("@/context/use-backups", () => ({ useBackups: () => ({ serverStorages }) }));

const v = (name: string, extra: Partial<DeclaredVariable> = {}): DeclaredVariable => ({
  name,
  required: false,
  encrypted: false,
  default: null,
  services: ["app"],
  ...extra,
});

const VIEW: TemplateView = {
  id: "t1",
  source: "org",
  slug: "",
  name: "twillingate",
  latest_revision: 2,
  withdrawn_at: null,
  revision: 2,
  body: { files: { "compose.yaml": "services: {}\n" }, summary: "Analytics for one tenant", description: "", icon: "chart", color: "" },
  detected: {
    variables: [
      v("MEMORY_MAX", { default: "256m" }),
      v("NAME", { required: true, pattern: "^[a-z]{3,20}$", description: "Instance name" }),
      v("SESSION_KEY", { required: true, encrypted: true }),
    ],
    expose: [
      { id: "app:8080/http", target: "app:8080", container: "app", port: 8080, protocol: "http" },
      { id: "app:9000/tcp", target: "app:9000", container: "app", port: 9000, protocol: "tcp" },
    ],
    backups: [{ name: "db", type: "sqlite" }],
    warnings: [],
  },
  note: "",
  created_at: "2026-10-12T10:00:00Z",
  created_by: "u1",
  revisions: [
    { revision: 2, note: "bump", created_at: "2026-10-12T10:00:00Z", created_by: "u1" },
    { revision: 1, note: "", created_at: "2026-10-11T10:00:00Z", created_by: "u1" },
  ],
};

const SUMMARY = { id: "t1", source: "org", slug: "", name: "twillingate", latest_revision: 2, instances: 0, behind: 0, withdrawn_at: null, updated_at: "" };
const CATALOG = {
  ...SUMMARY,
  id: "c1",
  source: "catalog",
  slug: "actual-budget",
  name: "Actual Budget",
  latest_revision: 3,
  catalog: {
    summary: "Plan your money",
    description: "Current catalog description",
    keywords: ["finance"],
    tags: [],
    alternative_to: ["mint"],
    license: "MIT",
    homepage_url: "https://actualbudget.org",
    repository_url: "",
    documentation_url: "",
    logo_url: "https://winterflow.io/catalog/logos/actual-budget.png",
  },
};
const CATALOG_VIEW: TemplateView = {
  ...VIEW,
  id: "c1",
  source: "catalog",
  slug: "actual-budget",
  name: "Actual Budget",
  latest_revision: 3,
  revision: 3,
  catalog: CATALOG.catalog,
  body: { ...VIEW.body, summary: "Frozen old summary", description: "Frozen old description", icon: "Box" },
  revisions: [{ revision: 3, note: "latest", created_at: "2026-10-16T10:00:00Z", created_by: "catalog" }, ...(VIEW.revisions ?? [])],
};

const waitFor_ = vi.fn();
const notifications = { connected: true, subscribe: () => () => {}, waitFor: waitFor_ } as NotificationsContextValue;

const routes = (extra: Record<string, unknown> = {}) => ({
  "/api/v1/template/get-templates": { templates: [SUMMARY] },
  "/api/v1/template/get-template": VIEW,
  "/api/v1/domains/check": { available: true, covered: false, registration: null, claims: null },
  "/api/v1/server/get-public-key": { public_key: "K1" },
  "/api/v1/instance/get-instance": { app_id: "app-9", key: "k" },
  "/api/v1/domain/get-domains": { domains: [{ name: "*.example.com", tls: "auto" }] },
  ...extra,
});

// accepted makes create-instance (or path) answer 202: the result comes over SSE.
function accepted(api: ReturnType<typeof fakeAPI>, path = "/api/v1/instance/create-instance") {
  const inner = api.fetchMock;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init?: RequestInit) => {
      const res = await inner(url, init);
      if (new URL(url, "http://x").pathname !== path || !res.ok) return res;
      return new Response(JSON.stringify({ data: { request_id: "r1" } }), { status: 202 });
    }),
  );
}

// held makes the API answer path only once release() is called.
function held(api: ReturnType<typeof fakeAPI>, path: string) {
  let release = () => {};
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  const inner = api.fetchMock;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init?: RequestInit) => {
      if (new URL(url, "http://x").pathname === path) await gate;
      return inner(url, init);
    }),
  );
  return release;
}

function renderFlow(initialTemplate = "t1") {
  return render(
    <MemoryRouter initialEntries={["/server/s1/new?from=template"]}>
      <NotificationsContext.Provider value={notifications}>
        <Routes>
          <Route path="/server/s1/new" element={<TemplateFlow serverId="s1" initialTemplate={initialTemplate} />} />
          <Route path="/server/s1/app/:appId" element={<p>the new app</p>} />
          <Route path="/templates" element={<p>templates</p>} />
        </Routes>
      </NotificationsContext.Provider>
    </MemoryRouter>,
  );
}

// fill gives the required values.
async function fill(user: ReturnType<typeof userEvent.setup>) {
  await user.type(await screen.findByLabelText("Instance name (NAME)"), "abc");
  await user.type(screen.getByLabelText("SESSION_KEY"), "s");
}

beforeEach(() => {
  toast.success.mockReset();
  toast.error.mockReset();
  refresh.mockClear();
  reconcile.mockReset();
  reconcile.mockImplementation(async () => []);
  noteDeployFailure.mockReset();
  ingress.on = false;
  apps.list = [];
  waitFor_.mockReset();
  serverStorages.mockImplementation(async () => ["tenants", "local"]);
});
afterEach(() => vi.unstubAllGlobals());

describe("TemplateFlow", () => {
  it("searches both sources by summary, keywords, and alternative-to", async () => {
    const user = userEvent.setup();
    fakeAPI(routes({
      "/api/v1/template/get-templates": { templates: [SUMMARY, CATALOG] },
      "/api/v1/template/get-template": (_: unknown, q: URLSearchParams) => q.get("id") === "c1" ? CATALOG_VIEW : VIEW,
    }));
    renderFlow("");
    const search = await screen.findByRole("searchbox", { name: "Search templates" });
    await user.type(search, "analytics");
    expect(await screen.findByRole("button", { name: /twillingate/ })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Actual Budget/ })).not.toBeInTheDocument();
    await user.clear(search);
    await user.type(search, "finance");
    expect(screen.getByRole("button", { name: /Actual Budget/ })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /twillingate/ })).not.toBeInTheDocument();
    await user.clear(search);
    await user.type(search, "mint");
    expect(screen.getByRole("button", { name: /Actual Budget/ })).toBeInTheDocument();
  });

  it("installs a catalog template using current metadata and the selected revision's form", async () => {
    const user = userEvent.setup();
    const newerCatalogView = {
      ...CATALOG_VIEW,
      catalog: { ...CATALOG_VIEW.catalog!, logo_url: "https://winterflow.io/catalog/logos/current.png" },
    };
    const api = fakeAPI(routes({
      "/api/v1/template/get-templates": { templates: [CATALOG] },
      "/api/v1/template/get-template": (_: unknown, q: URLSearchParams) =>
        q.get("revision") === "2" ? { ...newerCatalogView, revision: 2 } : newerCatalogView,
      "/api/v1/instance/create-instance": { request_id: "" },
    }));
    renderFlow("");
    await user.click(await screen.findByRole("button", { name: /Actual Budget/ }));
    await screen.findByText("Current catalog description");
    expect(document.querySelector('img[src*="catalog-logo"]')).toHaveAttribute("src", expect.stringContaining("current.png"));
    await user.click(screen.getByRole("button", { name: "Advanced" }));
    await user.selectOptions(screen.getByLabelText("Version"), "2");
    expect(await screen.findByText("Current catalog description")).toBeInTheDocument();
    expect(screen.queryByText("Frozen old description")).not.toBeInTheDocument();
    expect(screen.getByText("License: MIT")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Homepage" })).toHaveAttribute("href", "https://actualbudget.org");
    expect(screen.getByRole("link", { name: "Homepage" })).toHaveAttribute("rel", "noreferrer");
    await fill(user);
    await user.click(screen.getByRole("button", { name: "Install" }));
    expect(await screen.findByText("the new app")).toBeInTheDocument();
    expect(api.posted("/api/v1/instance/create-instance")[0]).toMatchObject({ template_id: "c1", revision: 2, name: "Actual Budget" });
  });

  it("does not open a withdrawn direct template link", async () => {
    fakeAPI(routes({ "/api/v1/template/get-templates": { templates: [SUMMARY, { ...CATALOG, withdrawn_at: "2026-10-16T00:00:00Z" }] } }));
    renderFlow("c1");
    expect(await screen.findByRole("alert")).toHaveTextContent("no longer available");
    expect(screen.getByRole("button", { name: /twillingate/ })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Actual Budget/ })).not.toBeInTheDocument();
  });

  it("uses a full-screen sheet for the picker on phones", async () => {
    vi.stubGlobal("matchMedia", (query: string) => ({
      matches: query === "(max-width: 639px)",
      media: query,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    }));
    const user = userEvent.setup();
    fakeAPI(routes({ "/api/v1/template/get-templates": { templates: [CATALOG] }, "/api/v1/template/get-template": CATALOG_VIEW }));
    renderFlow("");
    const sheet = await screen.findByRole("dialog", { name: "Choose a template" });
    expect(sheet).toHaveClass("!h-[100svh]", "!w-screen");
    await user.click(screen.getByRole("button", { name: /Actual Budget/ }));
    expect(await screen.findByLabelText("App name")).toBeInTheDocument();
    expect(screen.queryByRole("dialog", { name: "Choose a template" })).not.toBeInTheDocument();
  });

  it("opens a direct template link and asks for its required values first", async () => {
    const user = userEvent.setup();
    fakeAPI(routes());
    renderFlow();
    expect(await screen.findByText("twillingate")).toBeInTheDocument();
    expect(await screen.findByText("Analytics for one tenant")).toBeInTheDocument();
    const fields = await screen.findAllByRole("textbox");
    expect(fields[0]).toHaveAccessibleName("App name");
    expect(fields[1]).toHaveAccessibleName("Instance name (NAME)");
    // Optional values wait under More options, the compose file's shown.
    expect(screen.queryByLabelText("MEMORY_MAX")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "More options" }));
    expect(screen.getByLabelText("MEMORY_MAX")).toHaveAttribute("placeholder", "Default: 256m");
    // A pattern mismatch is a hint (the server decides); a $ is refused.
    await user.type(screen.getByLabelText("Instance name (NAME)"), "Shop");
    expect(screen.getByText("May not match ^[a-z]{3,20}$; the server checks it on save.")).toBeInTheDocument();
    expect(screen.getByLabelText("Instance name (NAME)")).not.toHaveAttribute("aria-invalid");
    await user.type(screen.getByLabelText("MEMORY_MAX"), "$x");
    expect(screen.getByLabelText("MEMORY_MAX")).toHaveAttribute("aria-invalid", "true");
    expect(screen.queryByText("Enter a value: this variable is required.")).not.toBeInTheDocument();
  });

  it("lists org templates before catalog templates, then opens the picked form", async () => {
    const user = userEvent.setup();
    const api = fakeAPI(
      routes({
        "/api/v1/template/get-templates": {
          templates: [SUMMARY, { ...SUMMARY, id: "t2", name: "blog", latest_revision: 4 }, { ...SUMMARY, id: "c1", source: "catalog", name: "Catalog app" }],
        },
        "/api/v1/template/get-template": (_: unknown, q: URLSearchParams) =>
          q.get("id") === "t2" ? { ...VIEW, id: "t2", name: "blog", latest_revision: 4, revision: 4 } : VIEW,
      }),
    );
    renderFlow("");
    expect(await screen.findByRole("searchbox", { name: "Search templates" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Catalog app/ })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Your templates" }).compareDocumentPosition(screen.getByRole("heading", { name: "Catalog" })) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    await user.click(screen.getByRole("button", { name: /blog/ }));
    expect(await screen.findByLabelText("Instance name (NAME)")).toBeInTheDocument();
    // Each card's look is read once per revision; the pick reads the latest.
    expect(api.calls.filter((c) => c.path === "/api/v1/template/get-template").map((c) => c.query)).toEqual(
      expect.arrayContaining(["?id=t1&revision=2", "?id=t2&revision=4", "?id=t2"]),
    );
  });

  it("starts with the template a template page's Create app picked", async () => {
    fakeAPI(
      routes({
        "/api/v1/template/get-templates": { templates: [SUMMARY, { ...SUMMARY, id: "t2", name: "blog", latest_revision: 4 }] },
        "/api/v1/template/get-template": (_: unknown, q: URLSearchParams) =>
          q.get("id") === "t2" ? { ...VIEW, id: "t2", name: "blog", latest_revision: 4, revision: 4 } : VIEW,
      }),
    );
    render(
      <MemoryRouter>
        <NotificationsContext.Provider value={notifications}>
          <TemplateFlow serverId="s1" initialTemplate="t2" />
        </NotificationsContext.Provider>
      </MemoryRouter>,
    );
    expect(await screen.findByText("blog")).toBeInTheDocument();
    expect(screen.queryByRole("searchbox", { name: "Search templates" })).not.toBeInTheDocument();
  });

  it("offers a hostname per http port on a server that routes them, checking its coverage", async () => {
    const user = userEvent.setup();
    ingress.on = true;
    const api = fakeAPI(routes({ "/api/v1/instance/create-instance": { request_id: "" } }));
    renderFlow();
    const host = await screen.findByLabelText("Hostname for app:8080");
    // The tcp port takes no hostname.
    expect(screen.queryByLabelText("Hostname for app:9000")).not.toBeInTheDocument();
    await user.type(host, "shop.twillingate.app");
    expect(await screen.findByText(/No domain on this server covers this hostname/)).toBeInTheDocument();
    expect(api.calls.filter((c) => c.path === "/api/v1/domains/check").at(-1)?.query).toBe("?server_id=s1&domain=shop.twillingate.app");
    await user.clear(host);
    await user.type(host, "bad_host!.com");
    await fill(user);
    await user.click(screen.getByRole("button", { name: "Install" }));
    expect(host).toHaveAccessibleDescription(/This isn't a valid hostname./);
    expect(api.posted("/api/v1/instance/create-instance")).toEqual([]);
    await user.clear(host);
    await user.type(host, "shop.example.com");
    await user.click(screen.getByRole("button", { name: "Install" }));
    await screen.findByText("the new app");
    expect(api.posted("/api/v1/instance/create-instance")[0]).toMatchObject({ hosts: [{ host: "shop.example.com", expose: "app:8080" }] });
  });

  it("asks for no hostname on a server without the built-in proxy", async () => {
    fakeAPI(routes());
    renderFlow();
    await screen.findByLabelText("Instance name (NAME)");
    expect(screen.queryByRole("heading", { name: "Hostnames" })).not.toBeInTheDocument();
  });

  it("creates the app with encrypted secrets, its name and a browser key, then opens it", async () => {
    const user = userEvent.setup();
    const api = fakeAPI(routes({ "/api/v1/instance/create-instance": { request_id: "r1" } }));
    accepted(api);
    waitFor_.mockResolvedValue({ status: 0 });
    renderFlow();
    await user.click(await screen.findByRole("button", { name: "Install" }));
    expect(screen.getAllByText("Enter a value: this variable is required.")).toHaveLength(2);
    await waitFor(() => expect(screen.getByLabelText("Instance name (NAME)")).toHaveFocus());
    await user.type(screen.getByLabelText("Instance name (NAME)"), "shop");
    await user.type(screen.getByLabelText("SESSION_KEY"), "hunter2");
    await user.click(screen.getByRole("button", { name: "Install" }));
    expect(await screen.findByText("the new app")).toBeInTheDocument();
    const [sent] = api.posted("/api/v1/instance/create-instance") as Record<string, unknown>[];
    expect(sent).toEqual({
      server_id: "s1",
      key: sent.key,
      template_id: "t1",
      revision: 2,
      name: "twillingate",
      values: { NAME: "shop" },
      secrets: { SESSION_KEY: "ct(K1):hunter2" },
    });
    expect(sent.key).toMatch(/^[0-9a-f-]{36}$/);
    expect(JSON.stringify(api.calls)).not.toContain('"hunter2"');
    expect(waitFor_).toHaveBeenCalledWith("r1", 10 * 60_000);
    expect(refresh).toHaveBeenCalled();
    expect(toast.success).toHaveBeenCalledWith("App created");
    expect(api.calls.find((c) => c.path === "/api/v1/instance/get-instance")?.query).toBe(`?server_id=s1&key=${sent.key}`);
  });

  it("names the app after the template, numbered when the server has one of that name", async () => {
    const user = userEvent.setup();
    apps.list = [{ id: "x", name: "twillingate", serverId: "s1" }, { id: "y", name: "twillingate 2", serverId: "other" }];
    const api = fakeAPI(routes({ "/api/v1/instance/create-instance": { request_id: "" } }));
    renderFlow();
    await fill(user);
    expect(screen.getByLabelText("App name")).toHaveValue("twillingate 2");
    await user.click(screen.getByRole("button", { name: "Install" }));
    expect(await screen.findByText("the new app")).toBeInTheDocument();
    expect(api.posted("/api/v1/instance/create-instance")[0]).toMatchObject({ name: "twillingate 2" });
  });

  it("warns about a name already used, and asks for one when it is cleared", async () => {
    const user = userEvent.setup();
    apps.list = [{ id: "x", name: "Blog", serverId: "s1" }];
    const api = fakeAPI(routes());
    renderFlow();
    await fill(user);
    const field = screen.getByLabelText("App name");
    await user.clear(field);
    await user.type(field, "blog");
    expect(screen.getByText("Another app on this server already has this name.")).toBeInTheDocument();
    await user.clear(field);
    await user.click(screen.getByRole("button", { name: "Install" }));
    expect(field).toHaveAccessibleDescription("Give the app a name.");
    expect(api.posted("/api/v1/instance/create-instance")).toEqual([]);
  });

  it("opens More options for an optional value that is wrong", async () => {
    const user = userEvent.setup();
    const api = fakeAPI(routes());
    renderFlow();
    await fill(user);
    await user.click(screen.getByRole("button", { name: "More options" }));
    await user.type(screen.getByLabelText("MEMORY_MAX"), "lo$ts");
    await user.click(screen.getByRole("button", { name: "More options" }));
    expect(screen.queryByLabelText("MEMORY_MAX")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Install" }));
    expect(screen.getByLabelText("MEMORY_MAX")).toHaveAccessibleDescription("A plain value can't contain $.");
    await waitFor(() => expect(screen.getByLabelText("MEMORY_MAX")).toHaveFocus());
    expect(screen.getByRole("alert")).toHaveTextContent("Some fields need attention.");
    expect(api.posted("/api/v1/instance/create-instance")).toEqual([]);
    // Fixing the value keeps the section open and the field focused.
    await user.clear(screen.getByLabelText("MEMORY_MAX"));
    await user.type(screen.getByLabelText("MEMORY_MAX"), "512m");
    expect(screen.getByLabelText("MEMORY_MAX")).toHaveValue("512m");
    expect(screen.getByLabelText("MEMORY_MAX")).toHaveFocus();
  });

  it("retries a failed deploy as an update of the same key, and a refused create as a create", async () => {
    const user = userEvent.setup();
    let refuse = true;
    const api = fakeAPI(
      routes({
        "/api/v1/instance/create-instance": () =>
          refuse ? new ApiError('invalid variable value: variable NAME: "abc" is taken', 400) : { request_id: "r1" },
        "/api/v1/instance/update-instance": { request_id: "r2" },
      }),
    );
    renderFlow();
    await fill(user);
    await user.click(screen.getByRole("button", { name: "Install" }));
    expect(await screen.findByRole("alert")).toHaveTextContent('"abc" is taken');

    refuse = false;
    accepted(api);
    waitFor_.mockResolvedValueOnce({ status: 1, error: "pull access denied" });
    await user.click(screen.getByRole("button", { name: "Install" }));
    expect(await screen.findByText("pull access denied")).toBeInTheDocument();
    // The template and version are fixed once the API holds the key.
    expect(screen.getByRole("button", { name: "Change template" })).toBeDisabled();
    await user.click(screen.getByRole("button", { name: "Advanced" }));
    expect(screen.getByLabelText("Version")).toBeDisabled();

    accepted(api, "/api/v1/instance/update-instance");
    waitFor_.mockResolvedValueOnce({ status: 0 });
    await user.click(screen.getByRole("button", { name: "Install" }));
    expect(await screen.findByText("the new app")).toBeInTheDocument();
    const creates = api.posted("/api/v1/instance/create-instance") as { key: string }[];
    const updates = api.posted("/api/v1/instance/update-instance") as { key: string; secrets: unknown }[];
    expect(creates).toHaveLength(2);
    expect(creates[0].key).toBe(creates[1].key);
    expect(updates).toEqual([expect.objectContaining({ key: creates[0].key, secrets: { SESSION_KEY: "ct(K1):s" } })]);
  });

  it("shows an agent failure by its last line, the rest behind Details", async () => {
    const user = userEvent.setup();
    const api = fakeAPI(routes({ "/api/v1/instance/create-instance": { request_id: "r1" } }));
    accepted(api);
    waitFor_.mockResolvedValueOnce({ status: 1, error: "Container web  Creating\nError: pull access denied\n" });
    renderFlow();
    await fill(user);
    await user.click(screen.getByRole("button", { name: "Install" }));
    expect(await screen.findByText("Error: pull access denied")).toBeVisible();
    expect(screen.getByRole("alert")).toContainElement(screen.getByText("Details"));
    expect(screen.getByText(/Container web Creating/)).not.toBeVisible();
  });

  it("opens the app a failed deploy left behind, noting why it failed", async () => {
    const user = userEvent.setup();
    const api = fakeAPI(
      routes({
        "/api/v1/instance/create-instance": { request_id: "r1" },
        "/api/v1/instance/get-instance": { app_id: "app-9", key: "k", state: "pending", last_error: "port is already allocated" },
      }),
    );
    accepted(api);
    reconcile.mockImplementation(async () => [{ id: "app-9" }]);
    waitFor_.mockResolvedValueOnce({ status: 1, error: "docker compose up: exit status 1: port is already allocated" });
    renderFlow();
    await fill(user);
    await user.click(screen.getByRole("button", { name: "Install" }));
    expect(await screen.findByText("the new app")).toBeInTheDocument();
    expect(noteDeployFailure).toHaveBeenCalledWith("app-9", "port is already allocated");
    expect(toast.error).toHaveBeenCalledWith("The app was created but couldn't start", {
      description: "port is already allocated",
    });
  });

  it("stays on the form when a refused create left nothing", async () => {
    const user = userEvent.setup();
    fakeAPI(routes({ "/api/v1/instance/create-instance": new ApiError("bad value", 400) }));
    renderFlow();
    await fill(user);
    await user.click(screen.getByRole("button", { name: "Install" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("bad value");
    expect(reconcile).not.toHaveBeenCalled();
  });

  it.each([500, 409])("retries a create the API answered %i as an update of the same key", async (status) => {
    const user = userEvent.setup();
    const api = fakeAPI(
      routes({
        "/api/v1/instance/create-instance": new ApiError("the key may be held", status),
        "/api/v1/instance/update-instance": { request_id: "r2" },
      }),
    );
    renderFlow();
    await fill(user);
    await user.click(screen.getByRole("button", { name: "Install" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("the key may be held");

    accepted(api, "/api/v1/instance/update-instance");
    waitFor_.mockResolvedValueOnce({ status: 0 });
    await user.click(screen.getByRole("button", { name: "Install" }));
    expect(await screen.findByText("the new app")).toBeInTheDocument();
    const creates = api.posted("/api/v1/instance/create-instance") as { key: string }[];
    const updates = api.posted("/api/v1/instance/update-instance") as { key: string }[];
    expect(creates).toHaveLength(1);
    expect(updates).toEqual([expect.objectContaining({ key: creates[0].key })]);
  });

  it("holds the cards' place with a skeleton while the templates are read", async () => {
    const api = fakeAPI(routes());
    const release = held(api, "/api/v1/template/get-templates");
    renderFlow();
    expect(screen.getByRole("status", { name: "Loading templates" })).toBeInTheDocument();
    release();
    expect(await screen.findByText("twillingate")).toBeInTheDocument();
    expect(screen.queryByRole("status", { name: "Loading templates" })).not.toBeInTheDocument();
  });

  it("keeps Create off until the server's storages are read", async () => {
    let resolve: (names: string[]) => void = () => {};
    serverStorages.mockImplementation(() => new Promise<string[]>((r) => (resolve = r)));
    fakeAPI(routes());
    renderFlow();
    await screen.findByLabelText("Instance name (NAME)");
    expect(screen.getByRole("button", { name: "Install" })).toBeDisabled();
    await act(async () => resolve(["tenants"]));
    expect(screen.getByRole("button", { name: "Install" })).toBeEnabled();
  });

  it("turns on a declared backup with a storage of this server; the rest start off", async () => {
    const user = userEvent.setup();
    const api = fakeAPI(routes({ "/api/v1/instance/create-instance": { request_id: "" } }));
    renderFlow();
    const storage = await screen.findByLabelText("Storage for db");
    expect(screen.getByRole("switch", { name: "Back up db" })).not.toBeChecked();
    expect(storage).toBeDisabled();
    await user.click(screen.getByRole("switch", { name: "Back up db" }));
    expect(storage).toHaveValue("tenants");
    await user.selectOptions(storage, "local");
    await fill(user);
    await user.click(screen.getByRole("button", { name: "Install" }));
    await screen.findByText("the new app");
    expect(api.posted("/api/v1/instance/create-instance")[0]).toMatchObject({ backups: { db: { enabled: true, storage: "local" } } });
  });

  it("holds the backup switches until the storages are read, then sends the storage shown", async () => {
    const user = userEvent.setup();
    let resolve: (names: string[]) => void = () => {};
    serverStorages.mockImplementation(() => new Promise<string[]>((r) => (resolve = r)));
    const api = fakeAPI(routes({ "/api/v1/instance/create-instance": { request_id: "" } }));
    renderFlow();
    await screen.findByLabelText("Storage for db");
    expect(screen.getByRole("switch", { name: "Back up db" })).toBeDisabled();
    await act(async () => resolve(["tenants", "local"]));
    const toggle = screen.getByRole("switch", { name: "Back up db" });
    expect(toggle).toBeEnabled();
    await user.click(toggle);
    expect(screen.getByLabelText("Storage for db")).toHaveValue("tenants");
    await fill(user);
    await user.click(screen.getByRole("button", { name: "Install" }));
    await screen.findByText("the new app");
    expect(api.posted("/api/v1/instance/create-instance")[0]).toMatchObject({ backups: { db: { enabled: true, storage: "tenants" } } });
  });

  it("refuses a backup turned on without a storage, linking to Storages", async () => {
    const user = userEvent.setup();
    serverStorages.mockImplementation(async () => []);
    const api = fakeAPI(routes());
    renderFlow();
    await screen.findByLabelText("Storage for db");
    expect(screen.getByRole("link", { name: "Add one" })).toHaveAttribute("href", "/server/s1/settings/storages");
    await user.click(screen.getByRole("switch", { name: "Back up db" }));
    await fill(user);
    await user.click(screen.getByRole("button", { name: "Install" }));
    expect(screen.getByLabelText("Storage for db")).toHaveAccessibleDescription("Pick a storage to keep its backups in.");
    expect(api.posted("/api/v1/instance/create-instance")).toEqual([]);
  });

  it("starts the next template untried and folded when the pick changes", async () => {
    const user = userEvent.setup();
    const optionalRequired = { ...VIEW, detected: { ...VIEW.detected, variables: [...(VIEW.detected.variables ?? []), v("LABEL")] } };
    fakeAPI(
      routes({
        "/api/v1/template/get-templates": { templates: [SUMMARY, { ...SUMMARY, id: "t2", name: "blog" }] },
        "/api/v1/template/get-template": (_: unknown, q: URLSearchParams) =>
          q.get("id") === "t2" ? { ...optionalRequired, id: "t2", name: "blog" } : optionalRequired,
      }),
    );
    renderFlow();
    await screen.findByLabelText("Instance name (NAME)");
    await user.type(await screen.findByLabelText("Instance name (NAME)"), "abc");
    await user.type(screen.getByLabelText("SESSION_KEY"), "s");
    await user.click(screen.getByRole("button", { name: "More options" }));
    await user.type(screen.getByLabelText("LABEL"), "$no");
    await user.click(screen.getByRole("button", { name: "Install" }));
    expect(screen.getByRole("alert")).toHaveTextContent("Some fields need attention.");

    await user.click(screen.getByRole("button", { name: "Change template" }));
    await user.click(screen.getByRole("button", { name: /blog/ }));
    await screen.findByLabelText("Instance name (NAME)");
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("LABEL")).not.toBeInTheDocument();
    expect(screen.queryByText("Enter a value: this variable is required.")).not.toBeInTheDocument();
  });

  it("loads an older version from Advanced", async () => {
    const user = userEvent.setup();
    const api = fakeAPI(routes());
    renderFlow();
    await screen.findByLabelText("Instance name (NAME)");
    await user.click(screen.getByRole("button", { name: "Advanced" }));
    expect(screen.getByRole("option", { name: "Latest (version 2)" })).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "Version 2 · bump" })).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "Version 1" })).toBeInTheDocument();
    expect(screen.queryByRole("option", { name: /\br\d/ })).not.toBeInTheDocument();
    await user.selectOptions(screen.getByLabelText("Version"), "1");
    await waitFor(() => expect(api.calls.at(-1)?.query).toBe("?id=t1&revision=1"));
  });

  it("points to Templates when the org has none, and shows load failures", async () => {
    fakeAPI(routes({ "/api/v1/template/get-templates": { templates: [] } }));
    renderFlow();
    expect(await screen.findByText("No templates are available to install yet.")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Go to Templates" })).toHaveAttribute("href", "/templates");
    vi.unstubAllGlobals();

    fakeAPI(routes({ "/api/v1/template/get-template": new Error("template not found") }));
    renderFlow();
    expect(await screen.findByRole("alert")).toHaveTextContent("template not found");
  });

  it("retries a failed load", async () => {
    const user = userEvent.setup();
    let fail = true;
    fakeAPI(routes({ "/api/v1/template/get-template": () => (fail ? new Error("bad gateway") : VIEW) }));
    renderFlow();
    expect(await screen.findByRole("alert")).toHaveTextContent("bad gateway");
    fail = false;
    await user.click(screen.getByRole("button", { name: "Retry" }));
    expect(await screen.findByLabelText("Instance name (NAME)")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("gives up on a read that never answers, aborting it, and offers Retry", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
      const signals: AbortSignal[] = [];
      vi.stubGlobal(
        "fetch",
        vi.fn((_url: string, init?: RequestInit) => {
          if (init?.signal) signals.push(init.signal);
          return new Promise<Response>(() => {});
        }),
      );
      renderFlow();
      act(() => {
        vi.advanceTimersByTime(LOAD_TIMEOUT_MS + 1);
      });
      expect(await screen.findByRole("alert")).toHaveTextContent("The server didn't answer in time");
      expect(signals[0]?.aborted).toBe(true);

      fakeAPI(routes());
      await user.click(screen.getByRole("button", { name: "Retry" }));
      expect(await screen.findByLabelText("Instance name (NAME)")).toBeInTheDocument();
    } finally {
      vi.useRealTimers();
    }
  });
});
