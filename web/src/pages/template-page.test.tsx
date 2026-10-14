import { act, cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { NotificationsContext, type NotificationsContextValue } from "@/context/notifications-context-base";
import { NotificationsProvider } from "@/context/notifications-context";
import { draftFromBody } from "@/lib/template-draft";
import { ApiError, type TemplateBody, type TemplateView } from "@/lib/templates";
import TemplatePage from "@/pages/template-page";
import { fakeAPI } from "@/test/fake-api";
import { rollout } from "@/test/rollouts";
import { flushPending } from "@/test/flush";

const toast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn(), warning: vi.fn() }));
vi.mock("sonner", () => ({ toast }));
const pageTitle = vi.hoisted(() => vi.fn());
vi.mock("@/layouts/use-page-title", () => ({
  usePageTitle: pageTitle,
  useCurrentPageTitle: () => "",
  useHeadingInView: () => ({ current: null }),
}));
vi.mock("@/context/use-auth", () => ({ useAuth: () => ({ isAuthenticated: true }) }));
const serverList = vi.hoisted(() => ({ servers: [{ id: "s1", name: "alpha" }] }));
vi.mock("@/context/use-servers", () => ({ useServers: () => ({ servers: serverList.servers, activeServerId: "s1" }) }));
vi.mock("@/components/code-editor", () => ({
  CodeEditor: ({ value, onChange, filename }: { value: string; onChange?: (v: string) => void; filename?: string }) => (
    <textarea aria-label={`Content of ${filename || "file"}`} value={value} onChange={(e) => onChange?.(e.target.value)} />
  ),
}));

const COMPOSE = `services:
  app:
    image: ghcr.io/dmtrkzntsv/twillingate:\${VERSION}
    ports: ["127.0.0.1:\${PORT}:8080"]
x-winterflow:
  backups:
    - { name: db, type: sqlite, volume: data, path: twillingate.db }
`;

const BODY: TemplateBody = {
  files: { "compose.yaml": COMPOSE },
  parameters: [
    { name: "NAME", required: true, pattern: "^[a-z]{3,20}$" },
    { name: "PORT", required: true },
    { name: "VERSION", required: true },
  ],
  app_name: "${NAME}",
  icon: "chart",
  color: "#2563eb",
  ingress: [{ host: "${NAME}.twillingate.app", port: "${PORT}" }],
  backups: { db: { enabled: true, storage: "tenants" } },
};

function view(revision: number): TemplateView {
  return {
    id: "t1",
    name: "twillingate",
    latest_revision: revision,
    revision,
    body: BODY,
    note: "",
    created_at: "2026-10-12T10:00:00Z",
    created_by: "u1",
    revisions: [{ revision, note: "", created_at: "2026-10-12T10:00:00Z", created_by: "u1" }],
  };
}

type Listener = Parameters<NotificationsContextValue["subscribe"]>[0];
const listeners = new Set<Listener>();
const notifications = {
  connected: true,
  subscribe: (fn: Listener) => {
    listeners.add(fn);
    return () => {
      listeners.delete(fn);
    };
  },
  waitFor: vi.fn(),
} as unknown as NotificationsContextValue;
// emit delivers an SSE notification to the page.
function emit(n: Parameters<Listener>[0]) {
  act(() => {
    for (const fn of listeners) fn(n);
  });
}

function renderPage(url: string, state?: unknown) {
  return render(
    <MemoryRouter initialEntries={[state ? { pathname: url, state } : url]}>
      <NotificationsContext.Provider value={notifications}>
        <Routes>
          <Route path="/templates" element={<p>the list</p>} />
          <Route path="/templates/new" element={<TemplatePage />} />
          <Route path="/templates/:templateId" element={<TemplatePage />} />
        </Routes>
      </NotificationsContext.Provider>
    </MemoryRouter>,
  );
}

beforeEach(() => {
  for (const f of Object.values(toast)) f.mockReset();
});
afterEach(() => vi.unstubAllGlobals());

describe("TemplatePage", () => {
  it("adds a template as version 1, then opens it", async () => {
    const user = userEvent.setup();
    const api = fakeAPI({
      "/api/v1/template/save-template": { id: "t9", name: "shop", revision: 1, warnings: ["SMTP_PASS is not a parameter"] },
      "/api/v1/template/get-template": { ...view(1), id: "t9", name: "shop" },
      "/api/v1/instance/get-instances": { instances: [] },
    });
    renderPage("/templates/new");
    await user.type(screen.getByLabelText("Template name"), "shop");
    await user.type(screen.getByLabelText("App name"), "Shop");
    await user.click(screen.getByRole("button", { name: "Save template" }));
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith("Saved version 1"));
    expect(toast.warning).toHaveBeenCalledWith("Check these references", { description: "SMTP_PASS is not a parameter" });
    const [sent] = api.posted("/api/v1/template/save-template") as { name: string; base_revision: number; id?: string }[];
    expect(sent).toMatchObject({ name: "shop", base_revision: 0 });
    expect(sent.id).toBeUndefined();
    expect(await screen.findByRole("heading", { name: "shop" })).toBeInTheDocument();
  });

  it("refuses to save what the API would refuse, naming it", async () => {
    const user = userEvent.setup();
    const api = fakeAPI({});
    renderPage("/templates/new");
    expect(pageTitle).toHaveBeenLastCalledWith("New template");
    // A new template saves as version 1: there is nothing to note yet.
    expect(screen.queryByLabelText("Version note")).not.toBeInTheDocument();
    await user.type(screen.getByLabelText("Template name"), "Shop");
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Save template" }));
    expect(screen.getByRole("alert")).toHaveTextContent("Name: lowercase");
    expect(toast.error).not.toHaveBeenCalled();
    expect(api.calls).toEqual([]);
    // The message follows the draft: the next problem, then none.
    await user.clear(screen.getByLabelText("Template name"));
    await user.type(screen.getByLabelText("Template name"), "shop");
    expect(screen.getByRole("alert")).toHaveTextContent("App name is required.");
    await user.type(screen.getByLabelText("App name"), "Shop");
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("adds a version on top of the one it read, with a note", async () => {
    const user = userEvent.setup();
    const api = fakeAPI({
      "/api/v1/template/get-template": view(3),
      "/api/v1/instance/get-instances": { instances: [] },
      "/api/v1/template/save-template": { id: "t1", name: "twillingate", revision: 4, warnings: null },
    });
    renderPage("/templates/t1");
    const save = await screen.findByRole("button", { name: "Save as version 4" });
    expect(save).toBeDisabled();
    await user.click(screen.getAllByRole("button", { name: /Add parameter/ })[0]);
    await user.type(screen.getAllByLabelText("Parameter name").at(-1)!, "MEMORY_MAX");
    await user.type(screen.getByLabelText("Version note"), "memory cap");
    await user.click(save);
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith("Saved version 4"));
    const [sent] = api.posted("/api/v1/template/save-template") as {
      id: string;
      base_revision: number;
      note: string;
      body: TemplateBody;
    }[];
    expect(sent).toMatchObject({ id: "t1", base_revision: 3, note: "memory cap" });
    expect(sent.body.parameters?.map((p) => p.name)).toEqual(["NAME", "PORT", "VERSION", "MEMORY_MAX"]);
    expect(sent.body.backups).toEqual({ db: { enabled: true, storage: "tenants" } });
    // The save re-reads the template only: its instances didn't change.
    await waitFor(() => expect(api.calls.filter((c) => c.path === "/api/v1/template/get-template")).toHaveLength(2));
    expect(api.calls.filter((c) => c.path === "/api/v1/instance/get-instances")).toHaveLength(1);
  });

  it("creates an app from the template on the server, asking which one when there are several", async () => {
    const user = userEvent.setup();
    fakeAPI({ "/api/v1/template/get-template": view(3), "/api/v1/instance/get-instances": { instances: [] } });
    const { unmount } = renderPage("/templates/t1");
    expect(await screen.findByRole("link", { name: "Create app" })).toHaveAttribute(
      "href",
      "/server/s1/new?from=template&template=t1",
    );
    unmount();
    serverList.servers = [
      { id: "s1", name: "alpha" },
      { id: "s2", name: "beta" },
    ];
    try {
      renderPage("/templates/t1");
      await user.click(await screen.findByRole("button", { name: "Create app" }));
      expect(await screen.findByRole("menuitem", { name: "On beta" })).toHaveAttribute(
        "href",
        "/server/s2/new?from=template&template=t1",
      );
    } finally {
      serverList.servers = [{ id: "s1", name: "alpha" }];
    }
  });

  it("is clean again when an edit is taken back", async () => {
    const user = userEvent.setup();
    fakeAPI({
      "/api/v1/template/get-template": view(3),
      "/api/v1/instance/get-instances": { instances: [] },
    });
    renderPage("/templates/t1");
    const save = await screen.findByRole("button", { name: "Save as version 4" });
    await user.type(screen.getByLabelText("App name"), "x");
    expect(save).toBeEnabled();
    await user.type(screen.getByLabelText("App name"), "{Backspace}");
    expect(save).toBeDisabled();
  });

  it("says who saved meanwhile on a concurrent edit, and reloads", async () => {
    const user = userEvent.setup();
    let latest = 3;
    fakeAPI({
      "/api/v1/template/get-template": () => view(latest),
      "/api/v1/instance/get-instances": { instances: [] },
      "/api/v1/template/save-template": () => {
        latest = 4;
        return new ApiError("someone saved the template meanwhile: reload it and save again", 409);
      },
    });
    renderPage("/templates/t1");
    await user.type(await screen.findByLabelText("App name"), "-x");
    await user.click(screen.getByRole("button", { name: "Save as version 4" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Someone saved version 4 meanwhile; reload");
    await user.click(screen.getByRole("button", { name: "Reload" }));
    expect(await screen.findByRole("button", { name: "Save as version 5" })).toBeInTheDocument();
    expect(screen.getByLabelText("App name")).toHaveValue("${NAME}");
    expect(toast.error).not.toHaveBeenCalled();
  });

  it("reports any other failed save", async () => {
    const user = userEvent.setup();
    fakeAPI({
      "/api/v1/template/get-template": view(3),
      "/api/v1/instance/get-instances": { instances: [] },
      "/api/v1/template/save-template": new Error("invalid template: files: x-winterflow.backups[0]: unknown type"),
    });
    renderPage("/templates/t1");
    await user.type(await screen.findByLabelText("App name"), "-x");
    await user.click(screen.getByRole("button", { name: "Save as version 4" }));
    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith("Couldn't save the template", {
        description: "invalid template: files: x-winterflow.backups[0]: unknown type",
      }),
    );
  });

  it("keeps unsaved edits when the event stream drops and reconnects", async () => {
    const streams: { onopen?: () => void; onerror?: () => void }[] = [];
    vi.stubGlobal(
      "EventSource",
      class {
        onopen?: () => void;
        onerror?: () => void;
        onmessage?: () => void;
        constructor() {
          streams.push(this);
        }
        close() {}
      },
    );
    const user = userEvent.setup();
    const api = fakeAPI({
      "/api/v1/template/get-template": view(3),
      "/api/v1/instance/get-instances": { instances: [] },
    });
    render(
      <MemoryRouter initialEntries={["/templates/t1"]}>
        <NotificationsProvider>
          <Routes>
            <Route path="/templates/:templateId" element={<TemplatePage />} />
          </Routes>
        </NotificationsProvider>
      </MemoryRouter>,
    );
    act(() => streams[0].onopen?.());
    await user.type(await screen.findByLabelText("App name"), "-x");
    for (const step of [() => streams[0].onerror?.(), () => streams[0].onopen?.()]) {
      act(step);
      await flushPending();
    }
    expect(screen.getByLabelText("App name")).toHaveValue("${NAME}-x");
    expect(screen.getByRole("button", { name: "Save as version 4" })).toBeEnabled();
    expect(api.calls.filter((c) => c.path === "/api/v1/template/get-template")).toHaveLength(1);
  });

  it("offers Delete only while no app uses the template, saying why", async () => {
    const user = userEvent.setup();
    fakeAPI({
      "/api/v1/template/get-template": view(3),
      "/api/v1/instance/get-instances": { instances: [{ key: "k1" }] },
    });
    renderPage("/templates/t1");
    expect(await screen.findByText("Version 3 · used by 1 app")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "More actions" }));
    const item = await screen.findByRole("menuitem", { name: /Delete template/ });
    expect(item).toHaveAttribute("aria-disabled", "true");
    // The reason sits beside the dimmed item, not inside it (contrast).
    const reason = screen.getByText("Delete its apps or detach them from it first");
    expect(item).not.toContainElement(reason);
  });

  it("deletes a template no app uses after a confirm", async () => {
    const user = userEvent.setup();
    const api = fakeAPI({
      "/api/v1/template/get-template": view(3),
      "/api/v1/instance/get-instances": { instances: [] },
      "/api/v1/template/delete-template": null,
    });
    renderPage("/templates/t1");
    await user.click(await screen.findByRole("button", { name: "More actions" }));
    await user.click(await screen.findByRole("menuitem", { name: /Delete template/ }));
    const dialog = await screen.findByRole("alertdialog");
    expect(dialog).toHaveTextContent("Delete twillingate?");
    expect(dialog).toHaveTextContent("detached");
    expect(api.posted("/api/v1/template/delete-template")).toEqual([]);
    await user.click(within(dialog).getByRole("button", { name: "Delete template" }));
    expect(await screen.findByText("the list")).toBeInTheDocument();
    expect(api.posted("/api/v1/template/delete-template")).toEqual([{ id: "t1" }]);
  });

  it("explains a refused delete and keeps the confirm open", async () => {
    const user = userEvent.setup();
    fakeAPI({
      "/api/v1/template/get-template": view(3),
      "/api/v1/instance/get-instances": { instances: [] },
      "/api/v1/template/delete-template": new ApiError("the template has live instances: delete or unlink them first", 409),
    });
    renderPage("/templates/t1");
    await user.click(await screen.findByRole("button", { name: "More actions" }));
    await user.click(await screen.findByRole("menuitem", { name: /Delete template/ }));
    const dialog = await screen.findByRole("alertdialog");
    await user.click(within(dialog).getByRole("button", { name: "Delete template" }));
    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith("Couldn't delete the template", {
        description: "the template has live instances: delete or unlink them first",
      }),
    );
    expect(screen.getByRole("alertdialog")).toBeInTheDocument();
    expect(screen.queryByText("the list")).not.toBeInTheDocument();
  });

  it("starts with a way back to Templates and opens old tab links on their new tabs", async () => {
    fakeAPI({
      "/api/v1/template/get-template": view(3),
      "/api/v1/instance/get-instances": { instances: [] },
    });
    renderPage("/templates/t1?tab=revisions");
    expect(await screen.findByRole("tab", { name: "Versions" })).toHaveAttribute("aria-selected", "true");
    expect(screen.getByRole("link", { name: "Templates" })).toHaveAttribute("href", "/templates");
    expect(screen.getByRole("heading", { level: 1, name: "twillingate" })).toBeInTheDocument();
    expect(screen.getByText("Version 3 · not used by any app yet")).toBeInTheDocument();
    cleanup();
    vi.unstubAllGlobals();

    fakeAPI({
      "/api/v1/template/get-template": view(3),
      "/api/v1/instance/get-instances": { instances: [] },
    });
    renderPage("/templates/t1?tab=instances");
    expect(await screen.findByRole("tab", { name: "Apps using it" })).toHaveAttribute("aria-selected", "true");
    expect(screen.getByText(/No app uses this template yet/)).toBeInTheDocument();
  });

  it("names a new template and says what saving does", () => {
    fakeAPI({});
    renderPage("/templates/new");
    expect(screen.getByRole("heading", { level: 1, name: "New template" })).toBeInTheDocument();
    expect(screen.getByText("Saving creates version 1.")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "More actions" })).not.toBeInTheDocument();
    expect(screen.queryByRole("tablist")).not.toBeInTheDocument();
  });

  it("shows Not found for another org's template, and retries other failures", async () => {
    fakeAPI({ "/api/v1/template/get-template": new ApiError("template not found", 404), "/api/v1/instance/get-instances": { instances: [] } });
    renderPage("/templates/t1");
    expect(await screen.findByText("Template not found")).toBeInTheDocument();
    vi.unstubAllGlobals();

    const user = userEvent.setup();
    let fail = true;
    fakeAPI({
      "/api/v1/template/get-template": () => (fail ? new Error("database down") : view(3)),
      "/api/v1/instance/get-instances": { instances: [] },
    });
    renderPage("/templates/t2");
    expect(await screen.findByText("database down")).toBeInTheDocument();
    fail = false;
    await user.click(screen.getByRole("button", { name: "Retry" }));
    expect(await screen.findAllByRole("heading", { name: "twillingate" })).not.toHaveLength(0);
  });

  it("opens prefilled from Save as template, with its notes", async () => {
    const draft = draftFromBody("shop-front", BODY);
    renderPage("/templates/new", {
      draft,
      notes: ["Redirects aren't part of templates; they were left out."],
      appName: "Shop front",
    });
    expect(screen.getByLabelText("Template name")).toHaveValue("shop-front");
    const banner = screen.getByText((_, el) => el?.tagName === "P" && !!el.textContent?.startsWith("Built from Shop front."));
    expect(banner).toHaveTextContent(
      "Built from Shop front. Replace what else differs per app, such as hostnames, with ${PARAM} parameters before saving.",
    );
    const note = screen.getByText("Redirects aren't part of templates; they were left out.");
    expect(banner.compareDocumentPosition(note) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(screen.getByRole("button", { name: "Save template" })).toBeEnabled();
  });

  it("has the apps and the versions in tabs; Update refreshes the apps only", async () => {
    const user = userEvent.setup();
    const at = (revision: number) => ({
      key: "k1",
      server_id: "s1",
      app_id: "a1",
      template_id: "t1",
      template_name: "twillingate",
      revision,
      parameters: { NAME: "bakomi", PORT: "8081", VERSION: "1.0" },
      secrets: null,
      state: "created",
      status_code: 1,
      last_error: "",
      created_at: "2026-10-12T10:00:00Z",
      updated_at: "2026-10-12T10:00:00Z",
    });
    let instances = [at(2)];
    const api = fakeAPI({
      "/api/v1/template/get-template": view(3),
      "/api/v1/instance/get-instances": () => ({ instances }),
      "/api/v1/instance/upgrade-instance": () => {
        instances = [at(3)];
        return { instance: at(3), request_id: "", warnings: [] };
      },
    });
    renderPage("/templates/t1?tab=apps");
    await user.click(await screen.findByRole("button", { name: "Update" }));
    await user.click(await screen.findByRole("button", { name: "Update to version 3" }));
    await waitFor(() => expect(screen.queryByRole("button", { name: "Update" })).not.toBeInTheDocument());
    expect(screen.getByText("Version 3")).toBeInTheDocument();
    // The editor read the template once (the dialog read version 2 to compare
    // defaults): the update didn't reload it.
    const reads = api.calls.filter((c) => c.path === "/api/v1/template/get-template");
    expect(reads.map((c) => c.query)).toEqual(["?id=t1", "?id=t1&revision=2"]);

    await user.click(screen.getByRole("tab", { name: "Versions" }));
    expect(await screen.findByRole("button", { name: "Changes from version 2" })).toBeInTheDocument();
    await user.click(screen.getByRole("tab", { name: "Editor" }));
    expect(screen.getByLabelText("Template name")).toHaveValue("twillingate");
  });

  it("updates all apps behind after a confirm, and shows the rollout", async () => {
    const user = userEvent.setup();
    const behind = { key: "k1", server_id: "s1", app_id: "a1", template_id: "t1", template_name: "twillingate", revision: 2,
      parameters: { NAME: "bakomi" }, secrets: null, state: "created", status_code: 1, last_error: "",
      created_at: "2026-10-12T10:00:00Z", updated_at: "2026-10-12T10:00:00Z" };
    const api = fakeAPI({
      "/api/v1/template/get-template": view(3),
      "/api/v1/instance/get-instances": { instances: [behind] },
      "/api/v1/rollout/get-rollouts": { rollouts: [] },
      "/api/v1/template/upgrade-instances": rollout({ state: "queued", current_app_id: "", done: 0, skipped: 0, total: 0 }),
    });
    renderPage("/templates/t1?tab=apps");
    await user.click(await screen.findByRole("button", { name: "Update all apps" }));
    await user.click(screen.getByRole("button", { name: "Update all" }));
    expect(await screen.findByText("Update to version 3 queued…")).toBeInTheDocument();
    expect(api.posted("/api/v1/template/upgrade-instances")).toEqual([{ id: "t1", revision: 3 }]);
    expect(api.calls.find((c) => c.path === "/api/v1/rollout/get-rollouts")?.query).toBe("?kind=template_upgrade&scope_key=t1");
    // One upgrade at a time: the button waits for this one.
    expect(screen.getByRole("button", { name: "Update all apps" })).toBeDisabled();
    // Queuing moved no instance: the list is not read again until a step.
    const listReads = () => api.calls.filter((c) => c.path === "/api/v1/instance/get-instances").length;
    expect(listReads()).toBe(1);
    emit({ type: "rollout", payload: rollout({ done: 1, skipped: 0 }) } as Parameters<Listener>[0]);
    await waitFor(() => expect(listReads()).toBe(2));
  });

  it("says why an update could not start", async () => {
    const user = userEvent.setup();
    fakeAPI({
      "/api/v1/template/get-template": view(3),
      "/api/v1/instance/get-instances": { instances: [{ key: "k1", server_id: "s1", app_id: "a1", revision: 1, state: "created", parameters: {} }] },
      "/api/v1/template/upgrade-instances": Object.assign(new Error("an upgrade of this template is already running"), { status: 409 }),
    });
    renderPage("/templates/t1?tab=apps");
    await user.click(await screen.findByRole("button", { name: "Update all apps" }));
    await user.click(screen.getByRole("button", { name: "Update all" }));
    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith("Couldn't start the update", { description: "an upgrade of this template is already running" }),
    );
  });
});
