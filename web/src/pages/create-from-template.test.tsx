import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Outlet, Route, Routes } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { NotificationsContext, type NotificationsContextValue } from "@/context/notifications-context-base";
import { ApiError, LOAD_TIMEOUT_MS, type TemplateView } from "@/lib/templates";
import CreateFromTemplatePage from "@/pages/create-from-template";
import { fakeAPI } from "@/test/fake-api";

const toast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn() }));
vi.mock("sonner", () => ({ toast }));
vi.mock("@/lib/ecies", () => ({
  ENCRYPTED: "<encrypted>",
  encryptSecret: vi.fn(async (value: string, key: string) => `ct(${key}):${value}`),
}));
vi.mock("@/context/use-servers", () => ({ useServers: () => ({ activeServerId: "s1" }) }));
const refresh = vi.hoisted(() => vi.fn(async () => {}));
vi.mock("@/context/use-apps", () => ({ useApps: () => ({ refresh }) }));
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
      { name: "MEMORY_MAX", default: "256m" },
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

const waitFor_ = vi.fn();
const notifications = { connected: true, subscribe: () => () => {}, waitFor: waitFor_ } as NotificationsContextValue;

const routes = (extra: Record<string, unknown> = {}) => ({
  "/api/v1/template/get-templates": {
    templates: [{ id: "t1", name: "twillingate", latest_revision: 2, instances: 0, behind: 0, updated_at: "" }],
  },
  "/api/v1/template/get-template": VIEW,
  "/api/v1/domains/check": { available: true, covered: false, registration: null, claims: null },
  "/api/v1/server/get-public-key": { public_key: "K1" },
  "/api/v1/instance/get-instance": { app_id: "app-9", key: "k" },
  ...extra,
});

// accepted makes create-instance answer 202 (the result comes over SSE).
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

function renderPage() {
  return render(
    <MemoryRouter initialEntries={["/server/s1/new"]}>
      <NotificationsContext.Provider value={notifications}>
        <Routes>
          <Route element={<Outlet />}>
            <Route path="/server/s1/new" element={<CreateFromTemplatePage />} />
            <Route path="/server/s1/app/:appId" element={<p>the new app</p>} />
            <Route path="/templates" element={<p>templates</p>} />
          </Route>
        </Routes>
      </NotificationsContext.Provider>
    </MemoryRouter>,
  );
}

beforeEach(() => {
  toast.success.mockReset();
  refresh.mockClear();
  waitFor_.mockReset();
});
afterEach(() => vi.unstubAllGlobals());

describe("CreateFromTemplatePage", () => {
  it("asks for required values first, checking patterns as typed", async () => {
    const user = userEvent.setup();
    fakeAPI(routes());
    renderPage();
    const fields = await screen.findAllByRole("textbox");
    expect(fields[0]).toHaveAccessibleName("NAME");
    expect(screen.getByLabelText("MEMORY_MAX")).toHaveAttribute("placeholder", "Default: 256m");
    await user.type(screen.getByLabelText("NAME"), "Shop");
    expect(screen.getByText("Must match ^[a-z]{3,20}$")).toBeInTheDocument();
    // Marked invalid (red border) and pointing at its message.
    expect(screen.getByLabelText("NAME")).toHaveAttribute("aria-invalid", "true");
    expect(screen.getByLabelText("NAME")).toHaveClass("border-destructive");
    expect(screen.getByLabelText("NAME")).toHaveAccessibleDescription("Must match ^[a-z]{3,20}$");
    expect(screen.getByLabelText("MEMORY_MAX")).not.toHaveAttribute("aria-invalid");
    expect(screen.queryByText("Required")).not.toBeInTheDocument();
  });

  it("previews the hosts with the domain coverage check", async () => {
    const user = userEvent.setup();
    const api = fakeAPI(routes());
    renderPage();
    expect(await screen.findByText("Fill in NAME.")).toBeInTheDocument();
    await user.type(screen.getByLabelText("NAME"), "shop");
    const hosts = screen.getByRole("list", { name: "Hosts" });
    expect(within(hosts).getByText("shop.twillingate.app")).toBeInTheDocument();
    expect(await within(hosts).findByText(/No domain on this server covers this hostname/)).toBeInTheDocument();
    const checks = api.calls.filter((c) => c.path === "/api/v1/domains/check");
    expect(checks.at(-1)?.query).toBe("?server_id=s1&domain=shop.twillingate.app");
    expect(screen.getByText("shop", { selector: "span.font-medium" })).toBeInTheDocument();
  });

  it("adds the app with encrypted secrets and a browser key, then opens it", async () => {
    const user = userEvent.setup();
    const api = fakeAPI(routes({ "/api/v1/instance/create-instance": { request_id: "r1" } }));
    accepted(api);
    waitFor_.mockResolvedValue({ status: 0 });
    renderPage();
    await user.click(await screen.findByRole("button", { name: "Add app" }));
    expect(screen.getAllByText("Required")).toHaveLength(2);
    await user.type(screen.getByLabelText("NAME"), "shop");
    await user.type(screen.getByLabelText("SESSION_KEY"), "hunter2");
    await user.selectOptions(screen.getByLabelText("Storage for db"), "local");
    await user.click(screen.getByRole("button", { name: "Add app" }));
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
    expect(api.calls.find((c) => c.path === "/api/v1/instance/get-instance")?.query).toBe(`?server_id=s1&key=${sent.key}`);
  });

  it("retries a failed deploy as an update of the same key, and a refused create as a create", async () => {
    const user = userEvent.setup();
    let refuse = true;
    const api = fakeAPI(
      routes({
        "/api/v1/instance/create-instance": () =>
          refuse ? new ApiError("invalid template: parameters.NAME: \"abc\" is taken", 400) : { request_id: "r1" },
        "/api/v1/instance/update-instance": { request_id: "r2" },
      }),
    );
    renderPage();
    await user.type(await screen.findByLabelText("NAME"), "abc");
    await user.type(screen.getByLabelText("SESSION_KEY"), "s");
    await user.click(screen.getByRole("button", { name: "Add app" }));
    expect(await screen.findByRole("alert")).toHaveTextContent('"abc" is taken');

    refuse = false;
    accepted(api);
    waitFor_.mockResolvedValueOnce({ status: 1, error: "pull access denied" });
    await user.click(screen.getByRole("button", { name: "Add app" }));
    expect(await screen.findByText("pull access denied")).toBeInTheDocument();
    expect(screen.getByLabelText("Template")).toBeDisabled();

    accepted(api, "/api/v1/instance/update-instance");
    waitFor_.mockResolvedValueOnce({ status: 0 });
    await user.click(screen.getByRole("button", { name: "Add app" }));
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
    renderPage();
    await user.type(await screen.findByLabelText("NAME"), "abc");
    await user.type(screen.getByLabelText("SESSION_KEY"), "s");
    await user.click(screen.getByRole("button", { name: "Add app" }));
    expect(await screen.findByText("Error: pull access denied")).toBeVisible();
    expect(screen.getByRole("alert")).toContainElement(screen.getByText("Details"));
    expect(screen.getByText(/Container web Creating/)).not.toBeVisible();
  });

  it.each([500, 409])("retries a create the API answered %i as an update of the same key", async (status) => {
    const user = userEvent.setup();
    const api = fakeAPI(
      routes({
        "/api/v1/instance/create-instance": new ApiError("the key may be held", status),
        "/api/v1/instance/update-instance": { request_id: "r2" },
      }),
    );
    renderPage();
    await user.type(await screen.findByLabelText("NAME"), "abc");
    await user.type(screen.getByLabelText("SESSION_KEY"), "s");
    await user.click(screen.getByRole("button", { name: "Add app" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("the key may be held");

    accepted(api, "/api/v1/instance/update-instance");
    waitFor_.mockResolvedValueOnce({ status: 0 });
    await user.click(screen.getByRole("button", { name: "Add app" }));
    expect(await screen.findByText("the new app")).toBeInTheDocument();
    const creates = api.posted("/api/v1/instance/create-instance") as { key: string }[];
    const updates = api.posted("/api/v1/instance/update-instance") as { key: string }[];
    expect(creates).toHaveLength(1);
    expect(updates).toEqual([expect.objectContaining({ key: creates[0].key })]);
  });

  it("offers No storage only when the template's backup has none by default", async () => {
    fakeAPI(routes());
    const { unmount } = renderPage();
    const select = await screen.findByLabelText("Storage for db");
    expect(within(select).queryByRole("option", { name: "No storage" })).not.toBeInTheDocument();
    unmount();
    vi.unstubAllGlobals();

    const bare = { ...VIEW, body: { ...VIEW.body, backups: { db: { enabled: false } } } };
    fakeAPI(routes({ "/api/v1/template/get-template": bare }));
    renderPage();
    const empty = await screen.findByLabelText("Storage for db");
    expect(within(empty).getByRole("option", { name: "No storage" })).toBeInTheDocument();
  });

  it("flags a template storage this server doesn't have", async () => {
    serverStorages.mockResolvedValueOnce(["local"]);
    fakeAPI(routes());
    renderPage();
    expect(await screen.findByText("This server has no storage named tenants.", { exact: false })).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "tenants (not on this server)" })).toBeInTheDocument();
  });

  it("loads an older revision on request", async () => {
    const user = userEvent.setup();
    const api = fakeAPI(routes());
    renderPage();
    await user.selectOptions(await screen.findByLabelText("Revision"), "1");
    await waitFor(() => expect(api.calls.at(-1)?.query).toBe("?id=t1&revision=1"));
  });

  it("points to Templates when the org has none, and shows load failures", async () => {
    fakeAPI(routes({ "/api/v1/template/get-templates": { templates: [] } }));
    renderPage();
    expect(await screen.findByRole("link", { name: "Add one" })).toHaveAttribute("href", "/templates");
    vi.unstubAllGlobals();

    fakeAPI(routes({ "/api/v1/template/get-template": new Error("template not found") }));
    renderPage();
    expect(await screen.findByRole("alert")).toHaveTextContent("template not found");
  });

  it("retries a failed load", async () => {
    const user = userEvent.setup();
    let fail = true;
    fakeAPI(
      routes({
        "/api/v1/template/get-template": () => (fail ? new Error("bad gateway") : VIEW),
      }),
    );
    renderPage();
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
      // A browser out of connections to the host queues the request
      // without ever answering it.
      const signals: AbortSignal[] = [];
      vi.stubGlobal(
        "fetch",
        vi.fn((_url: string, init?: RequestInit) => {
          if (init?.signal) signals.push(init.signal);
          return new Promise<Response>(() => {});
        }),
      );
      renderPage();
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
