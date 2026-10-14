import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { TemplateFlow } from "@/components/new-app/template-flow";
import { NotificationsContext, type NotificationsContextValue } from "@/context/notifications-context-base";
import { ApiError, LOAD_TIMEOUT_MS, type TemplateView } from "@/lib/templates";
import { fakeAPI } from "@/test/fake-api";

const toast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn() }));
vi.mock("sonner", () => ({ toast }));
vi.mock("@/lib/ecies", () => ({
  ENCRYPTED: "<encrypted>",
  encryptSecret: vi.fn(async (value: string, key: string) => `ct(${key}):${value}`),
}));
vi.mock("@/context/use-servers", () => ({ useServers: () => ({ activeServerId: "s1" }) }));
const refresh = vi.hoisted(() => vi.fn(async () => {}));
const reconcile = vi.hoisted(() => vi.fn(async (): Promise<{ id: string }[]> => []));
const noteDeployFailure = vi.hoisted(() => vi.fn());
vi.mock("@/context/use-apps", () => ({ useApps: () => ({ refresh, reconcile, noteDeployFailure }) }));
const serverStorages = vi.hoisted(() => vi.fn(async () => ["tenants", "local"]));
vi.mock("@/context/use-backups", () => ({ useBackups: () => ({ serverStorages }) }));

const VIEW: TemplateView = {
  id: "t1",
  name: "twillingate",
  latest_revision: 2,
  revision: 2,
  body: {
    files: { "compose.yaml": "services: {}\n" },
    parameters: [
      { name: "MEMORY_MAX", default: "256m", pattern: "^[0-9]+m$" },
      { name: "NAME", required: true, pattern: "^[a-z]{3,20}$", description: "Instance name" },
      { name: "SESSION_KEY", secret: true, required: true },
    ],
    app_name: "${NAME}",
    icon: "chart",
    color: "",
    ingress: [{ host: "${NAME}.twillingate.app", port: 8080 }],
    backups: { db: { enabled: true, storage: "tenants" } },
  },
  note: "",
  created_at: "2026-10-12T10:00:00Z",
  created_by: "u1",
  revisions: [
    { revision: 2, note: "bump", created_at: "2026-10-12T10:00:00Z", created_by: "u1" },
    { revision: 1, note: "", created_at: "2026-10-11T10:00:00Z", created_by: "u1" },
  ],
};

const SUMMARY = { id: "t1", name: "twillingate", latest_revision: 2, instances: 0, behind: 0, updated_at: "" };

const waitFor_ = vi.fn();
const notifications = { connected: true, subscribe: () => () => {}, waitFor: waitFor_ } as NotificationsContextValue;

const routes = (extra: Record<string, unknown> = {}) => ({
  "/api/v1/template/get-templates": { templates: [SUMMARY] },
  "/api/v1/template/get-template": VIEW,
  "/api/v1/domains/check": { available: true, covered: false, registration: null, claims: null },
  "/api/v1/server/get-public-key": { public_key: "K1" },
  "/api/v1/instance/get-instance": { app_id: "app-9", key: "k" },
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

function renderFlow() {
  return render(
    <MemoryRouter initialEntries={["/server/s1/new?from=template"]}>
      <NotificationsContext.Provider value={notifications}>
        <Routes>
          <Route path="/server/s1/new" element={<TemplateFlow serverId="s1" />} />
          <Route path="/server/s1/app/:appId" element={<p>the new app</p>} />
          <Route path="/templates" element={<p>templates</p>} />
        </Routes>
      </NotificationsContext.Provider>
    </MemoryRouter>,
  );
}

// fill gives the required values.
async function fill(user: ReturnType<typeof userEvent.setup>) {
  await user.type(await screen.findByLabelText("NAME"), "abc");
  await user.type(screen.getByLabelText("SESSION_KEY"), "s");
}

beforeEach(() => {
  toast.success.mockReset();
  toast.error.mockReset();
  refresh.mockClear();
  reconcile.mockReset();
  reconcile.mockImplementation(async () => []);
  noteDeployFailure.mockReset();
  waitFor_.mockReset();
  serverStorages.mockImplementation(async () => ["tenants", "local"]);
});
afterEach(() => vi.unstubAllGlobals());

describe("TemplateFlow", () => {
  it("picks the only template and asks for its required values first", async () => {
    const user = userEvent.setup();
    fakeAPI(routes());
    renderFlow();
    expect(await screen.findByRole("button", { name: /twillingate/ })).toHaveAttribute("aria-pressed", "true");
    const fields = await screen.findAllByRole("textbox");
    expect(fields[0]).toHaveAccessibleName("NAME");
    // Optional values wait under More options, their defaults shown.
    expect(screen.queryByLabelText("MEMORY_MAX")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "More options" }));
    expect(screen.getByLabelText("MEMORY_MAX")).toHaveAttribute("placeholder", "Default: 256m");
    await user.type(screen.getByLabelText("NAME"), "Shop");
    expect(screen.getByText("Must match ^[a-z]{3,20}$")).toBeInTheDocument();
    expect(screen.getByLabelText("NAME")).toHaveAttribute("aria-invalid", "true");
    expect(screen.getByLabelText("NAME")).toHaveAccessibleDescription("Must match ^[a-z]{3,20}$");
    expect(screen.getByLabelText("MEMORY_MAX")).not.toHaveAttribute("aria-invalid");
    expect(screen.queryByText("Required")).not.toBeInTheDocument();
  });

  it("waits for a pick when there are several templates", async () => {
    const user = userEvent.setup();
    const api = fakeAPI(
      routes({
        "/api/v1/template/get-templates": { templates: [SUMMARY, { ...SUMMARY, id: "t2", name: "blog", latest_revision: 4 }] },
        "/api/v1/template/get-template": (_: unknown, q: URLSearchParams) =>
          q.get("id") === "t2" ? { ...VIEW, id: "t2", name: "blog", latest_revision: 4, revision: 4 } : VIEW,
      }),
    );
    renderFlow();
    expect(await screen.findByText("Pick a template to continue.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /blog/ })).toHaveTextContent("Version 4");
    await user.click(screen.getByRole("button", { name: /blog/ }));
    expect(await screen.findByLabelText("NAME")).toBeInTheDocument();
    expect(api.calls.find((c) => c.path === "/api/v1/template/get-template")?.query).toBe("?id=t2");
  });

  it("previews the name and the address with the domain coverage check", async () => {
    const user = userEvent.setup();
    const api = fakeAPI(routes());
    renderFlow();
    expect(await screen.findByText("Fill in NAME to see the address.")).toBeInTheDocument();
    await user.type(screen.getByLabelText("NAME"), "shop");
    const addresses = screen.getByRole("list", { name: "Addresses" });
    expect(within(addresses).getByText("shop.twillingate.app")).toBeInTheDocument();
    expect(within(addresses).getByText(/Will be available at/)).toBeInTheDocument();
    expect(await within(addresses).findByText(/No domain on this server covers this hostname/)).toBeInTheDocument();
    const checks = api.calls.filter((c) => c.path === "/api/v1/domains/check");
    expect(checks.at(-1)?.query).toBe("?server_id=s1&domain=shop.twillingate.app");
    expect(screen.getByText("shop", { selector: "span.font-medium" })).toBeInTheDocument();
  });

  it("creates the app with encrypted secrets and a browser key, then opens it", async () => {
    const user = userEvent.setup();
    const api = fakeAPI(routes({ "/api/v1/instance/create-instance": { request_id: "r1" } }));
    accepted(api);
    waitFor_.mockResolvedValue({ status: 0 });
    renderFlow();
    await user.click(await screen.findByRole("button", { name: "Create app" }));
    expect(screen.getAllByText("Required")).toHaveLength(2);
    await waitFor(() => expect(screen.getByLabelText("NAME")).toHaveFocus());
    await user.type(screen.getByLabelText("NAME"), "shop");
    await user.type(screen.getByLabelText("SESSION_KEY"), "hunter2");
    await user.selectOptions(screen.getByLabelText("Storage for db"), "local");
    await user.click(screen.getByRole("button", { name: "Create app" }));
    expect(await screen.findByText("the new app")).toBeInTheDocument();
    const [sent] = api.posted("/api/v1/instance/create-instance") as Record<string, unknown>[];
    expect(sent).toMatchObject({
      server_id: "s1",
      template_id: "t1",
      revision: 2,
      parameters: { NAME: "shop" },
      secrets: { SESSION_KEY: "ct(K1):hunter2" },
      backups: { db: "local" },
    });
    expect(sent.key).toMatch(/^[0-9a-f-]{36}$/);
    expect(JSON.stringify(api.calls)).not.toContain('"hunter2"');
    expect(waitFor_).toHaveBeenCalledWith("r1", 10 * 60_000);
    expect(refresh).toHaveBeenCalled();
    expect(toast.success).toHaveBeenCalledWith("App created");
    expect(api.calls.find((c) => c.path === "/api/v1/instance/get-instance")?.query).toBe(`?server_id=s1&key=${sent.key}`);
  });

  it("opens More options for an optional value that is wrong", async () => {
    const user = userEvent.setup();
    const api = fakeAPI(routes());
    renderFlow();
    await fill(user);
    await user.click(screen.getByRole("button", { name: "More options" }));
    await user.type(screen.getByLabelText("MEMORY_MAX"), "lots");
    await user.click(screen.getByRole("button", { name: "More options" }));
    expect(screen.queryByLabelText("MEMORY_MAX")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Create app" }));
    expect(screen.getByLabelText("MEMORY_MAX")).toHaveAccessibleDescription("Must match ^[0-9]+m$");
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
          refuse ? new ApiError('invalid template: parameters.NAME: "abc" is taken', 400) : { request_id: "r1" },
        "/api/v1/instance/update-instance": { request_id: "r2" },
      }),
    );
    renderFlow();
    await fill(user);
    await user.click(screen.getByRole("button", { name: "Create app" }));
    expect(await screen.findByRole("alert")).toHaveTextContent('"abc" is taken');

    refuse = false;
    accepted(api);
    waitFor_.mockResolvedValueOnce({ status: 1, error: "pull access denied" });
    await user.click(screen.getByRole("button", { name: "Create app" }));
    expect(await screen.findByText("pull access denied")).toBeInTheDocument();
    // The template and version are fixed once the API holds the key.
    expect(screen.getByRole("button", { name: /twillingate/ })).toBeDisabled();
    await user.click(screen.getByRole("button", { name: "Advanced" }));
    expect(screen.getByLabelText("Version")).toBeDisabled();

    accepted(api, "/api/v1/instance/update-instance");
    waitFor_.mockResolvedValueOnce({ status: 0 });
    await user.click(screen.getByRole("button", { name: "Create app" }));
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
    await user.click(screen.getByRole("button", { name: "Create app" }));
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
    await user.click(screen.getByRole("button", { name: "Create app" }));
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
    await user.click(screen.getByRole("button", { name: "Create app" }));
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
    await user.click(screen.getByRole("button", { name: "Create app" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("the key may be held");

    accepted(api, "/api/v1/instance/update-instance");
    waitFor_.mockResolvedValueOnce({ status: 0 });
    await user.click(screen.getByRole("button", { name: "Create app" }));
    expect(await screen.findByText("the new app")).toBeInTheDocument();
    const creates = api.posted("/api/v1/instance/create-instance") as { key: string }[];
    const updates = api.posted("/api/v1/instance/update-instance") as { key: string }[];
    expect(creates).toHaveLength(1);
    expect(updates).toEqual([expect.objectContaining({ key: creates[0].key })]);
  });

  it("keeps a template's own storage, offering No storage only for a backup without one", async () => {
    fakeAPI(routes());
    const { unmount } = renderFlow();
    const select = await screen.findByLabelText("Storage for db");
    expect(select).toHaveValue("tenants");
    expect(within(select).queryByRole("option", { name: "No storage" })).not.toBeInTheDocument();
    unmount();
    vi.unstubAllGlobals();

    const bare = { ...VIEW, body: { ...VIEW.body, backups: { db: { enabled: false } } } };
    fakeAPI(routes({ "/api/v1/template/get-template": bare }));
    renderFlow();
    const empty = await screen.findByLabelText("Storage for db");
    expect(within(empty).getByRole("option", { name: "No storage" })).toBeInTheDocument();
  });

  it("holds the cards' place with a skeleton while the templates are read", async () => {
    const api = fakeAPI(routes());
    const release = held(api, "/api/v1/template/get-templates");
    renderFlow();
    expect(screen.getByRole("status", { name: "Loading templates" })).toBeInTheDocument();
    release();
    expect(await screen.findByRole("button", { name: /twillingate/ })).toBeInTheDocument();
    expect(screen.queryByRole("status", { name: "Loading templates" })).not.toBeInTheDocument();
  });

  it("keeps Create off until the server's storages are read", async () => {
    const user = userEvent.setup();
    let resolve: (names: string[]) => void = () => {};
    serverStorages.mockImplementation(() => new Promise<string[]>((r) => (resolve = r)));
    const bare = { ...VIEW, body: { ...VIEW.body, backups: { db: { enabled: true } } } };
    const api = fakeAPI(routes({ "/api/v1/template/get-template": bare, "/api/v1/instance/create-instance": { request_id: "" } }));
    renderFlow();
    await screen.findByLabelText("NAME");
    expect(screen.getByRole("button", { name: "Create app" })).toBeDisabled();
    await act(async () => resolve(["tenants"]));
    expect(screen.getByRole("button", { name: "Create app" })).toBeEnabled();
    await fill(user);
    await user.click(screen.getByRole("button", { name: "Create app" }));
    await screen.findByText("the new app");
    expect(api.posted("/api/v1/instance/create-instance")[0]).toMatchObject({ backups: { db: "tenants" } });
  });

  it("starts the next template untried and folded when the pick changes", async () => {
    const user = userEvent.setup();
    const optionalRequired = {
      ...VIEW,
      body: {
        ...VIEW.body,
        parameters: [...(VIEW.body.parameters ?? []), { name: "LABEL", pattern: "^x+$" }],
      },
    };
    fakeAPI(
      routes({
        "/api/v1/template/get-templates": { templates: [SUMMARY, { ...SUMMARY, id: "t2", name: "blog" }] },
        "/api/v1/template/get-template": (_: unknown, q: URLSearchParams) =>
          q.get("id") === "t2" ? { ...optionalRequired, id: "t2", name: "blog" } : optionalRequired,
      }),
    );
    renderFlow();
    await user.click(await screen.findByRole("button", { name: /twillingate/ }));
    await user.type(await screen.findByLabelText("NAME"), "abc");
    await user.type(screen.getByLabelText("SESSION_KEY"), "s");
    await user.click(screen.getByRole("button", { name: "More options" }));
    await user.type(screen.getByLabelText("LABEL"), "no");
    await user.click(screen.getByRole("button", { name: "Create app" }));
    expect(screen.getByRole("alert")).toHaveTextContent("Some fields need attention.");

    await user.click(screen.getByRole("button", { name: /blog/ }));
    await screen.findByLabelText("NAME");
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("LABEL")).not.toBeInTheDocument();
    expect(screen.queryByText("Required")).not.toBeInTheDocument();
  });

  it("preselects this server's first storage for a backup without a default, and sends it", async () => {
    const user = userEvent.setup();
    const bare = { ...VIEW, body: { ...VIEW.body, backups: { db: { enabled: true } } } };
    const api = fakeAPI(routes({ "/api/v1/template/get-template": bare, "/api/v1/instance/create-instance": { request_id: "" } }));
    renderFlow();
    await waitFor(() => expect(screen.getByLabelText("Storage for db")).toHaveValue("tenants"));
    await fill(user);
    await user.click(screen.getByRole("button", { name: "Create app" }));
    await screen.findByText("the new app");
    expect(api.posted("/api/v1/instance/create-instance")[0]).toMatchObject({ backups: { db: "tenants" } });
  });

  it("sends no storage when the server has none", async () => {
    const user = userEvent.setup();
    serverStorages.mockImplementation(async () => []);
    const bare = { ...VIEW, body: { ...VIEW.body, backups: { db: { enabled: true } } } };
    const api = fakeAPI(routes({ "/api/v1/template/get-template": bare, "/api/v1/instance/create-instance": { request_id: "" } }));
    renderFlow();
    expect(await screen.findByLabelText("Storage for db")).toHaveValue("");
    await fill(user);
    await user.click(screen.getByRole("button", { name: "Create app" }));
    await screen.findByText("the new app");
    expect(api.posted("/api/v1/instance/create-instance")[0]).not.toHaveProperty("backups");
  });

  it("flags a template storage this server doesn't have, linking to Storages", async () => {
    serverStorages.mockImplementation(async () => ["local"]);
    fakeAPI(routes());
    renderFlow();
    expect(await screen.findByText("This server has no storage named tenants.", { exact: false })).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "tenants (not on this server)" })).toBeInTheDocument();
    expect(screen.getByLabelText("Storage for db")).toHaveValue("tenants");
    expect(screen.getByRole("link", { name: "Add it" })).toHaveAttribute("href", "/server/s1/settings/storages");
  });

  it("loads an older version from Advanced", async () => {
    const user = userEvent.setup();
    const api = fakeAPI(routes());
    renderFlow();
    await screen.findByLabelText("NAME");
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
    expect(await screen.findByText("Your organization has no templates yet.")).toBeInTheDocument();
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
    expect(await screen.findByLabelText("NAME")).toBeInTheDocument();
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
      expect(await screen.findByLabelText("NAME")).toBeInTheDocument();
    } finally {
      vi.useRealTimers();
    }
  });
});
