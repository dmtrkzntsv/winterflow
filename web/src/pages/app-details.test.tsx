import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Link, MemoryRouter, Outlet, Route, Routes, useLocation } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { AppsContext, type App, type AppsContextValue } from "@/context/apps-context-base";
import { NotificationsContext, type NotificationsContextValue } from "@/context/notifications-context-base";
import AppDetailsPage from "@/pages/app-details";
import type { BackupHealth } from "@/lib/backups";
import type { InstanceView, TemplateView } from "@/lib/templates";
import type { DraftState } from "@/pages/template-page";
import { fakeAPI } from "@/test/fake-api";

const toast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn(), warning: vi.fn() }));
vi.mock("sonner", () => ({ toast }));
vi.mock("@/components/app-backups", () => ({ AppBackups: () => <p>the backups tab</p> }));
vi.mock("@/components/app-metrics", () => ({ AppMetricBlocks: () => null, AppMetricsTab: () => null }));
// The stale shared-variables badge has its own tests (it subscribes to
// notifications).
vi.mock("@/components/stale-vars-badge", () => ({ StaleVarsBadge: () => null }));
vi.mock("@/components/app-editor-panel", () => ({ AppEditorPanel: () => <p>the plain editor</p> }));
vi.mock("@/components/code-editor", () => ({
  CodeEditor: ({ value, filename }: { value: string; filename?: string }) => (
    <pre aria-label={`Content of ${filename}`}>{value}</pre>
  ),
}));
vi.mock("@/lib/ecies", () => ({
  ENCRYPTED: "<encrypted>",
  encryptSecret: vi.fn(async (value: string, key: string) => `ct(${key}):${value}`),
}));
// Links point into the active server's scope (/server/:id/...).
vi.mock("@/context/use-servers", () => ({ useServers: () => ({ activeServerId: "srv-1" }) }));

const remove = vi.fn();
const refresh = vi.fn(async () => {});
const notifications = { connected: true, subscribe: () => () => {}, waitFor: vi.fn() } as NotificationsContextValue;

// NewTemplate stands in for the template editor Save as template opens.
function NewTemplate() {
  const state = useLocation().state as DraftState;
  return <p>new template {state.draft.name} with {state.draft.parameters.length} parameters</p>;
}

function renderPage(
  url: string,
  backupsByApp: Record<string, BackupHealth[]> = {},
  over: { app?: Partial<App>; ctx?: Partial<AppsContextValue> } = {},
) {
  const value = {
    apps: [{ id: "a1", serverId: "s", name: "Shop", version: "3", icon: "", color: "", createdAt: "", ...over.app }],
    statusByApp: { a1: 1 },
    backupsByApp,
    control: vi.fn(),
    remove,
    rename: vi.fn(),
    refresh,
    ...over.ctx,
  } as unknown as AppsContextValue;
  return render(
    <MemoryRouter initialEntries={[url]}>
      <NotificationsContext.Provider value={notifications}>
        <AppsContext.Provider value={value}>
          <Routes>
            <Route
              element={
                <>
                  <Link to="/app/a2?tab=editor">go to a2</Link>
                  <Outlet context={{ setBreadcrumbs: () => {} }} />
                </>
              }
            >
              <Route path="/app/:appId" element={<AppDetailsPage />} />
              <Route path="/server/srv-1" element={<p>the apps</p>} />
              <Route path="/org/templates/new" element={<NewTemplate />} />
            </Route>
          </Routes>
        </AppsContext.Provider>
      </NotificationsContext.Provider>
    </MemoryRouter>,
  );
}

describe("AppDetailsPage", () => {
  beforeEach(() => {
    remove.mockReset();
    refresh.mockClear();
    for (const f of Object.values(toast)) f.mockReset();
  });

  it("marks the Backups tab when a backup is late or failing", async () => {
    renderPage("/app/a1?tab=backups", { a1: [{ name: "db", state: "failing", last_error: "disk full" }] });
    const tab = await screen.findByRole("tab", { name: /Backups/ });
    expect(tab.querySelector('[aria-label="A backup is late or failing"]')).not.toBeNull();
    expect(screen.getByText("the backups tab")).toBeInTheDocument();
  });

  it("has no mark while the backups are fine", async () => {
    renderPage("/app/a1?tab=backups", { a1: [{ name: "db", state: "ok" }] });
    const tab = await screen.findByRole("tab", { name: /Backups/ });
    expect(tab.querySelector('[aria-label="A backup is late or failing"]')).toBeNull();
  });

  it("shows progress while an app is deleted, and where its backups stayed", async () => {
    const user = userEvent.setup({ delay: null });
    let finish: (v: unknown) => void = () => {};
    remove.mockReturnValue(new Promise((resolve) => (finish = resolve)));
    renderPage("/app/a1?tab=settings", { a1: [{ name: "db", state: "ok" }] });
    await user.click(await screen.findByRole("button", { name: /Delete Shop/ }));
    await user.click(screen.getByRole("button", { name: "Delete app" }));
    expect(await screen.findByRole("button", { name: /Deleting Shop/ })).toBeDisabled();
    expect(screen.getByRole("status")).toHaveTextContent("This can take a few minutes");
    expect(remove).toHaveBeenCalledWith("a1", { deleteVolumes: false, deleteBackups: false });
    await act(async () => finish({ warnings: [], volumesDeleted: false, backupsDeleted: false }));
    await waitFor(() =>
      expect(toast.success).toHaveBeenCalledWith("App deleted", { description: expect.stringContaining("Other apps' backups") }),
    );
    // The deleted app's page is left for the list.
    expect(await screen.findByText("the apps")).toBeInTheDocument();
  });

  it("reports a failed delete and a delete with warnings", async () => {
    const user = userEvent.setup({ delay: null });
    remove.mockRejectedValueOnce(new Error("timed out"));
    renderPage("/app/a1?tab=settings");
    await user.click(await screen.findByRole("button", { name: /Delete Shop/ }));
    await user.click(screen.getByRole("button", { name: "Delete app" }));
    await waitFor(() => expect(toast.error).toHaveBeenCalledWith("Delete failed", { description: "timed out" }));
    remove.mockResolvedValueOnce({ warnings: ["volume x is in use"], volumesDeleted: false, backupsDeleted: false });
    await user.click(screen.getByRole("button", { name: /Delete Shop/ }));
    await user.click(screen.getByRole("button", { name: "Delete app" }));
    await waitFor(() => expect(toast.warning).toHaveBeenCalledWith("App deleted with cleanup warnings", { description: "volume x is in use" }));
  });
});

const INSTANCE: InstanceView = {
  key: "k1",
  server_id: "s",
  app_id: "a1",
  template_id: "t1",
  template_name: "twillingate",
  revision: 2,
  parameters: { NAME: "bakomi" },
  secrets: { SESSION_KEY: "<encrypted>" },
  state: "created",
  status_code: 1,
  last_error: "",
  created_at: "2026-10-12T10:00:00Z",
  updated_at: "2026-10-12T10:00:00Z",
};

const template = (revision: number, extra: TemplateView["body"]["parameters"] = []): TemplateView => ({
  id: "t1",
  name: "twillingate",
  latest_revision: 3,
  revision,
  body: {
    files: { "compose.yaml": "services:\n  app:\n    image: twillingate:${VERSION}\n" },
    parameters: [{ name: "NAME", required: true }, { name: "SESSION_KEY", secret: true, required: true }, ...(extra ?? [])],
    app_name: "${NAME}",
    icon: "",
    color: "",
    ingress: null,
    backups: null,
  },
  note: "",
  created_at: "2026-10-12T10:00:00Z",
  created_by: "u1",
  revisions: null,
});

// instanceAPI answers the instance lookup: the instance, its revision and
// the latest (which adds a required REGION).
function instanceAPI(inst: InstanceView = INSTANCE, extra: Record<string, unknown> = {}) {
  return fakeAPI({
    "/api/v1/instance/get-instances": { instances: [inst] },
    "/api/v1/template/get-template": (_: unknown, q: URLSearchParams) =>
      q.get("revision") ? template(Number(q.get("revision"))) : template(3, [{ name: "REGION", required: true }]),
    "/api/v1/server/get-public-key": { public_key: "K" },
    ...extra,
  });
}

const managed = { app: { serverId: "s", templateId: "t1" } };

describe("AppDetailsPage of a template instance", () => {
  afterEach(() => vi.unstubAllGlobals());
  beforeEach(() => {
    refresh.mockClear();
    for (const f of Object.values(toast)) f.mockReset();
  });

  it("badges the template and revision, and upgrades asking for the new value", async () => {
    const user = userEvent.setup();
    const api = instanceAPI(INSTANCE, { "/api/v1/instance/upgrade-instance": { instance: INSTANCE, request_id: "", warnings: [] } });
    renderPage("/app/a1?tab=backups", {}, managed);
    expect(await screen.findByRole("link", { name: "twillingate · r2" })).toHaveAttribute("href", "/org/templates/t1");
    expect(screen.getByText("r3 available")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Upgrade to r3" }));
    const dialog = await screen.findByRole("dialog");
    await user.type(within(dialog).getByLabelText("REGION"), "eu");
    await user.click(within(dialog).getByRole("button", { name: "Upgrade to r3" }));
    await waitFor(() => expect(refresh).toHaveBeenCalled());
    expect(api.posted("/api/v1/instance/upgrade-instance")).toEqual([
      { server_id: "s", key: "k1", revision: 3, parameters: { REGION: "eu" } },
    ]);
  });

  it("shows the files read-only and a Parameters form instead of the editor", async () => {
    const user = userEvent.setup();
    const api = instanceAPI(INSTANCE, { "/api/v1/instance/update-instance": { instance: INSTANCE, request_id: "", warnings: [] } });
    renderPage("/app/a1?tab=editor", {}, managed);
    expect(await screen.findByText(/Managed by template/)).toBeInTheDocument();
    expect(screen.queryByText("the plain editor")).not.toBeInTheDocument();
    expect(screen.getByLabelText("Content of compose.yaml")).toHaveTextContent("image: twillingate:${VERSION}");
    const save = screen.getByRole("button", { name: "Save & redeploy" });
    expect(save).toBeDisabled();
    expect(screen.getByLabelText("SESSION_KEY")).toHaveAttribute("placeholder", "Stored, leave blank to keep");
    await user.clear(screen.getByLabelText("NAME"));
    await user.type(screen.getByLabelText("NAME"), "shop");
    await user.click(save);
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith("Saved and redeployed"));
    expect(api.posted("/api/v1/instance/update-instance")).toEqual([{ server_id: "s", key: "k1", parameters: { NAME: "shop" } }]);
    expect(refresh).toHaveBeenCalled();
  });

  it("replaces a secret only when a new one is typed, encrypted for the server", async () => {
    const user = userEvent.setup();
    const api = instanceAPI(INSTANCE, { "/api/v1/instance/update-instance": { instance: INSTANCE, request_id: "", warnings: [] } });
    renderPage("/app/a1?tab=editor", {}, managed);
    await user.type(await screen.findByLabelText("SESSION_KEY"), "new-secret");
    await user.click(screen.getByRole("button", { name: "Save & redeploy" }));
    await waitFor(() => expect(toast.success).toHaveBeenCalled());
    expect(api.posted("/api/v1/instance/update-instance")).toEqual([
      { server_id: "s", key: "k1", secrets: { SESSION_KEY: "ct(K):new-secret" } },
    ]);
  });

  it("shows a pending instance's failure and asks for its secrets again", async () => {
    const user = userEvent.setup();
    instanceAPI({ ...INSTANCE, state: "pending", last_error: "pull access denied" });
    renderPage("/app/a1?tab=editor", {}, managed);
    expect(await screen.findByText(/last deploy from its template failed: pull access denied/)).toBeInTheDocument();
    expect(screen.getByText("Not created yet")).toBeInTheDocument();
    expect(screen.getByLabelText("SESSION_KEY")).toHaveAttribute("placeholder", "Encrypted in your browser");
    await user.click(screen.getByRole("button", { name: "Save & redeploy" }));
    expect(screen.getByText("Required")).toBeInTheDocument();
  });

  it("never offers the plain editor while the instance is being looked up", async () => {
    vi.stubGlobal("fetch", vi.fn(() => new Promise(() => {})));
    renderPage("/app/a1?tab=editor", {}, managed);
    expect(await screen.findByRole("tab", { name: "Configuration" })).toBeInTheDocument();
    expect(screen.queryByText("the plain editor")).not.toBeInTheDocument();
  });

  it("offers Retry when the lookup fails", async () => {
    const user = userEvent.setup();
    fakeAPI({ "/api/v1/instance/get-instances": new Error("database down") });
    renderPage("/app/a1?tab=editor", {}, managed);
    expect(await screen.findByText("database down")).toBeInTheDocument();
    vi.unstubAllGlobals();
    instanceAPI();
    await user.click(screen.getByRole("button", { name: "Retry" }));
    expect(await screen.findByText(/Managed by template/)).toBeInTheDocument();
  });

  it("treats an app whose instance is gone as an ordinary app", async () => {
    fakeAPI({ "/api/v1/instance/get-instances": { instances: [] } });
    renderPage("/app/a1?tab=editor", {}, managed);
    expect(await screen.findByText("the plain editor")).toBeInTheDocument();
  });

  it("does not offer Save as template for an app a template manages", async () => {
    instanceAPI(INSTANCE);
    renderPage("/app/a1?tab=settings", {}, managed);
    expect(await screen.findByRole("button", { name: "Unlink from template" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Save as template" })).not.toBeInTheDocument();
  });

  it("unlinks from the template after confirming", async () => {
    const user = userEvent.setup();
    const api = instanceAPI(INSTANCE, { "/api/v1/instance/unlink-instance": { instance: INSTANCE, request_id: "", warnings: [] } });
    renderPage("/app/a1?tab=settings", {}, managed);
    await user.click(await screen.findByRole("button", { name: "Unlink from template" }));
    expect(
      screen.getByText("The app becomes an ordinary app you can edit freely; template upgrades no longer reach it."),
    ).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Unlink" }));
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith("Unlinked from the template"));
    expect(api.posted("/api/v1/instance/unlink-instance")).toEqual([{ server_id: "s", key: "k1" }]);
    expect(refresh).toHaveBeenCalled();
  });

  it("reports a failed unlink", async () => {
    const user = userEvent.setup();
    instanceAPI(INSTANCE, { "/api/v1/instance/unlink-instance": new Error("instance not found") });
    renderPage("/app/a1?tab=settings", {}, managed);
    await user.click(await screen.findByRole("button", { name: "Unlink from template" }));
    await user.click(screen.getByRole("button", { name: "Unlink" }));
    await waitFor(() => expect(toast.error).toHaveBeenCalledWith("Unlink failed", { description: "instance not found" }));
  });

  it("offers no Roll back in History: the template owns the files", async () => {
    instanceAPI();
    const getRevisions = vi.fn(async () => ({
      current: "bbbbbbbb",
      deployed: "bbbbbbbb",
      revisions: [
        { hash: "bbbbbbbb", subject: "upgrade", timestamp: 2 },
        { hash: "aaaaaaaa", subject: "create", timestamp: 1 },
      ],
    }));
    renderPage("/app/a1?tab=history", {}, { ...managed, ctx: { getRevisions } });
    expect(await screen.findByText(/Managed by template twillingate/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Roll back/ })).not.toBeInTheDocument();
  });

  it("re-reads the app list when a roll back is refused as managed", async () => {
    const user = userEvent.setup();
    const getRevisions = vi.fn(async () => ({
      current: "bbbbbbbb",
      deployed: "bbbbbbbb",
      revisions: [
        { hash: "bbbbbbbb", subject: "edit", timestamp: 2 },
        { hash: "aaaaaaaa", subject: "create", timestamp: 1 },
      ],
    }));
    const rollback = vi.fn(async () => {
      throw new Error("this app is managed by template twillingate: update the instance or unlink it");
    });
    renderPage("/app/a1?tab=history", {}, { ctx: { getRevisions, rollback } });
    await user.click(await screen.findByRole("button", { name: /Roll back/ }));
    await user.click(screen.getByRole("button", { name: "Roll back" }));
    await waitFor(() => expect(refresh).toHaveBeenCalled());
  });

  it("offers no Roll back while a templated app's instance is still being looked up", async () => {
    vi.stubGlobal("fetch", vi.fn(() => new Promise(() => {})));
    const getRevisions = vi.fn(async () => ({
      current: "bbbbbbbb",
      deployed: "bbbbbbbb",
      revisions: [
        { hash: "bbbbbbbb", subject: "edit", timestamp: 2 },
        { hash: "aaaaaaaa", subject: "create", timestamp: 1 },
      ],
    }));
    renderPage("/app/a1?tab=history", {}, { ...managed, ctx: { getRevisions } });
    expect(await screen.findByText(/Managed by a template/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Roll back/ })).not.toBeInTheDocument();
  });

  it("reports a template that can't be loaded for an upgrade", async () => {
    const user = userEvent.setup();
    instanceAPI(INSTANCE, {
      "/api/v1/template/get-template": (_: unknown, q: URLSearchParams) => {
        if (q.get("revision")) return template(2);
        return new Error("boom");
      },
    });
    renderPage("/app/a1?tab=backups", {}, managed);
    await user.click(await screen.findByRole("button", { name: "Upgrade to r3" }));
    await waitFor(() => expect(toast.error).toHaveBeenCalledWith("Couldn't load the template", { description: "boom" }));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("shows a failed save of the parameters", async () => {
    const user = userEvent.setup();
    instanceAPI(INSTANCE, { "/api/v1/instance/update-instance": new Error("render failed") });
    renderPage("/app/a1?tab=editor", {}, managed);
    await user.clear(await screen.findByLabelText("NAME"));
    await user.type(screen.getByLabelText("NAME"), "shop");
    await user.click(screen.getByRole("button", { name: "Save & redeploy" }));
    expect(await screen.findByText("render failed")).toBeInTheDocument();
  });

  it("names the lookup failure when the error has no message", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => Promise.reject("offline")));
    renderPage("/app/a1?tab=editor", {}, managed);
    expect(await screen.findByText("Failed to load the app's template")).toBeInTheDocument();
  });

  it("never shows the first app's instance on the app the user moved to", async () => {
    const user = userEvent.setup();
    let answer: () => void = () => {};
    const reply = (data: unknown) => new Response(JSON.stringify({ success: true, message: "ok", data }));
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string) => {
        const path = new URL(url, "http://x").pathname;
        if (path === "/api/v1/instance/get-instances") {
          await new Promise<void>((resolve) => (answer = resolve));
          return reply({ instances: [INSTANCE] });
        }
        if (path === "/api/v1/template/get-template") return reply(template(2));
        return new Response("{}", { status: 404 });
      }),
    );
    const other = { id: "a2", serverId: "s", name: "Blog", version: "1", icon: "", color: "", createdAt: "" };
    renderPage("/app/a1?tab=editor", {}, {
      ctx: {
        apps: [
          { id: "a1", serverId: "s", name: "Shop", version: "3", icon: "", color: "", createdAt: "", templateId: "t1" },
          other,
        ],
      },
    });
    await user.click(await screen.findByRole("link", { name: "go to a2" }));
    expect(await screen.findByText("the plain editor")).toBeInTheDocument();
    await act(async () => answer());
    // a1's late answer must not reach a2's page.
    await new Promise((r) => setTimeout(r, 50));
    expect(screen.getByText("the plain editor")).toBeInTheDocument();
    expect(screen.queryByText(/Managed by template/)).not.toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "twillingate · r2" })).not.toBeInTheDocument();
    expect(screen.queryByLabelText("NAME")).not.toBeInTheDocument();
  });
});

describe("AppDetailsPage: Save as template", () => {
  beforeEach(() => {
    for (const f of Object.values(toast)) f.mockReset();
  });

  it("opens a new template's editor built from the app", async () => {
    const user = userEvent.setup();
    const getApp = vi.fn(async () => ({
      app: {
        app_id: "a1",
        config: btoa(JSON.stringify({ name: "Shop", files: [{ filename: "compose.yml" }], variables: [{ name: "PORT" }] })),
        files: [{ name: "compose.yml", content: btoa("services: {}\n") }],
        variables: [{ name: "PORT", content: btoa("8080") }],
      },
      revision: 1,
      available_revisions: null,
    }));
    renderPage("/app/a1?tab=settings", {}, { ctx: { getApp } });
    await user.click(await screen.findByRole("button", { name: "Save as template" }));
    expect(await screen.findByText("new template shop with 1 parameters")).toBeInTheDocument();
  });

  it("reports an app it can't read", async () => {
    const user = userEvent.setup();
    const getApp = vi.fn(async () => Promise.reject(new Error("agent offline")));
    renderPage("/app/a1?tab=settings", {}, { ctx: { getApp } });
    await user.click(await screen.findByRole("button", { name: "Save as template" }));
    await waitFor(() => expect(toast.error).toHaveBeenCalledWith("Couldn't read the app", { description: "agent offline" }));
    expect(screen.getByRole("button", { name: "Save as template" })).toBeEnabled();
  });
});
