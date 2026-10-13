import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { NotificationsContext, type NotificationsContextValue } from "@/context/notifications-context-base";
import { NotificationsProvider } from "@/context/notifications-context";
import { draftFromBody } from "@/lib/template-draft";
import { ApiError, type TemplateBody, type TemplateView } from "@/lib/templates";
import TemplatePage from "@/pages/template-page";
import { fakeAPI } from "@/test/fake-api";

const toast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn(), warning: vi.fn() }));
vi.mock("sonner", () => ({ toast }));
const crumbs = vi.hoisted(() => vi.fn());
vi.mock("@/layouts/use-app-layout", () => ({ useAppBreadcrumbs: crumbs }));
vi.mock("@/context/use-auth", () => ({ useAuth: () => ({ isAuthenticated: true }) }));
vi.mock("@/context/use-servers", () => ({ useServers: () => ({ servers: [{ id: "s1", name: "alpha" }] }) }));
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

const notifications = { connected: true, subscribe: () => () => {}, waitFor: vi.fn() } as NotificationsContextValue;

function renderPage(url: string, state?: unknown) {
  return render(
    <MemoryRouter initialEntries={[state ? { pathname: url, state } : url]}>
      <NotificationsContext.Provider value={notifications}>
        <Routes>
          <Route path="/org/templates" element={<p>the list</p>} />
          <Route path="/org/templates/new" element={<TemplatePage />} />
          <Route path="/org/templates/:templateId" element={<TemplatePage />} />
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
  it("adds a template as revision 1, then opens it", async () => {
    const user = userEvent.setup();
    const api = fakeAPI({
      "/api/v1/template/save-template": { id: "t9", name: "shop", revision: 1, warnings: ["SMTP_PASS is not a parameter"] },
      "/api/v1/template/get-template": { ...view(1), id: "t9", name: "shop" },
      "/api/v1/instance/get-instances": { instances: [] },
    });
    renderPage("/org/templates/new");
    await user.type(screen.getByLabelText("Template name"), "shop");
    await user.type(screen.getByLabelText("App name"), "Shop");
    await user.click(screen.getByRole("button", { name: "Save template" }));
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith("Saved r1"));
    expect(toast.warning).toHaveBeenCalledWith("Check these references", { description: "SMTP_PASS is not a parameter" });
    const [sent] = api.posted("/api/v1/template/save-template") as { name: string; base_revision: number; id?: string }[];
    expect(sent).toMatchObject({ name: "shop", base_revision: 0 });
    expect(sent.id).toBeUndefined();
    expect(await screen.findByRole("heading", { name: "shop" })).toBeInTheDocument();
  });

  it("refuses to save what the API would refuse, naming it", async () => {
    const user = userEvent.setup();
    const api = fakeAPI({});
    renderPage("/org/templates/new");
    expect(crumbs).toHaveBeenLastCalledWith([{ label: "Templates", href: "/org/templates" }, { label: "Add template" }]);
    // A new template saves as revision 1: there is nothing to note yet.
    expect(screen.queryByLabelText("Revision note")).not.toBeInTheDocument();
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

  it("adds a revision on top of the one it read, with a note", async () => {
    const user = userEvent.setup();
    const api = fakeAPI({
      "/api/v1/template/get-template": view(3),
      "/api/v1/instance/get-instances": { instances: [] },
      "/api/v1/template/save-template": { id: "t1", name: "twillingate", revision: 4, warnings: null },
    });
    renderPage("/org/templates/t1");
    const save = await screen.findByRole("button", { name: "Save as r4" });
    expect(save).toBeDisabled();
    await user.click(screen.getAllByRole("button", { name: /Add parameter/ })[0]);
    await user.type(screen.getAllByLabelText("Parameter name").at(-1)!, "MEMORY_MAX");
    await user.type(screen.getByLabelText("Revision note"), "memory cap");
    await user.click(save);
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith("Saved r4"));
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

  it("is clean again when an edit is taken back", async () => {
    const user = userEvent.setup();
    fakeAPI({
      "/api/v1/template/get-template": view(3),
      "/api/v1/instance/get-instances": { instances: [] },
    });
    renderPage("/org/templates/t1");
    const save = await screen.findByRole("button", { name: "Save as r4" });
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
    renderPage("/org/templates/t1");
    await user.type(await screen.findByLabelText("App name"), "-x");
    await user.click(screen.getByRole("button", { name: "Save as r4" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Someone saved r4 meanwhile; reload");
    await user.click(screen.getByRole("button", { name: "Reload" }));
    expect(await screen.findByRole("button", { name: "Save as r5" })).toBeInTheDocument();
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
    renderPage("/org/templates/t1");
    await user.type(await screen.findByLabelText("App name"), "-x");
    await user.click(screen.getByRole("button", { name: "Save as r4" }));
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
      <MemoryRouter initialEntries={["/org/templates/t1"]}>
        <NotificationsProvider>
          <Routes>
            <Route path="/org/templates/:templateId" element={<TemplatePage />} />
          </Routes>
        </NotificationsProvider>
      </MemoryRouter>,
    );
    act(() => streams[0].onopen?.());
    await user.type(await screen.findByLabelText("App name"), "-x");
    for (const step of [() => streams[0].onerror?.(), () => streams[0].onopen?.()]) {
      act(step);
      await act(() => new Promise((r) => setTimeout(r, 20)));
    }
    expect(screen.getByLabelText("App name")).toHaveValue("${NAME}-x");
    expect(screen.getByRole("button", { name: "Save as r4" })).toBeEnabled();
    expect(api.calls.filter((c) => c.path === "/api/v1/template/get-template")).toHaveLength(1);
  });

  it("offers Delete only without live instances", async () => {
    fakeAPI({
      "/api/v1/template/get-template": view(3),
      "/api/v1/instance/get-instances": { instances: [{ key: "k1" }] },
    });
    renderPage("/org/templates/t1");
    expect(await screen.findByRole("button", { name: "Delete template" })).toBeDisabled();
    expect(screen.getByText("Revision 3 · 1 app uses it")).toBeInTheDocument();
  });

  it("deletes a template no app uses", async () => {
    const user = userEvent.setup();
    const api = fakeAPI({
      "/api/v1/template/get-template": view(3),
      "/api/v1/instance/get-instances": { instances: [] },
      "/api/v1/template/delete-template": null,
    });
    renderPage("/org/templates/t1");
    await user.click(await screen.findByRole("button", { name: "Delete template" }));
    await user.click(screen.getByRole("button", { name: "Delete" }));
    expect(await screen.findByText("the list")).toBeInTheDocument();
    expect(api.posted("/api/v1/template/delete-template")).toEqual([{ id: "t1" }]);
  });

  it("explains a refused delete", async () => {
    const user = userEvent.setup();
    fakeAPI({
      "/api/v1/template/get-template": view(3),
      "/api/v1/instance/get-instances": { instances: [] },
      "/api/v1/template/delete-template": new ApiError("the template has live instances: delete or unlink them first", 409),
    });
    renderPage("/org/templates/t1");
    await user.click(await screen.findByRole("button", { name: "Delete template" }));
    await user.click(screen.getByRole("button", { name: "Delete" }));
    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith("Couldn't delete the template", {
        description: "the template has live instances: delete or unlink them first",
      }),
    );
  });

  it("shows Not found for another org's template, and retries other failures", async () => {
    fakeAPI({ "/api/v1/template/get-template": new ApiError("template not found", 404), "/api/v1/instance/get-instances": { instances: [] } });
    renderPage("/org/templates/t1");
    expect(await screen.findByText("Template not found")).toBeInTheDocument();
    vi.unstubAllGlobals();

    const user = userEvent.setup();
    let fail = true;
    fakeAPI({
      "/api/v1/template/get-template": () => (fail ? new Error("database down") : view(3)),
      "/api/v1/instance/get-instances": { instances: [] },
    });
    renderPage("/org/templates/t2");
    expect(await screen.findByText("database down")).toBeInTheDocument();
    fail = false;
    await user.click(screen.getByRole("button", { name: "Retry" }));
    expect(await screen.findAllByRole("heading", { name: "twillingate" })).not.toHaveLength(0);
  });

  it("opens prefilled from Save as template, with its notes", async () => {
    const draft = draftFromBody("shop-front", BODY);
    renderPage("/org/templates/new", {
      draft,
      notes: ["Redirects aren't part of templates; they were left out."],
      appName: "Shop front",
    });
    expect(screen.getByLabelText("Template name")).toHaveValue("shop-front");
    const banner = screen.getByText((_, el) => el?.tagName === "P" && !!el.textContent?.startsWith("Built from Shop front."));
    expect(banner).toHaveTextContent(
      "Built from Shop front. Replace the parts that differ per app — hosts, ports — with ${PARAM} parameters before saving.",
    );
    const note = screen.getByText("Redirects aren't part of templates; they were left out.");
    expect(banner.compareDocumentPosition(note) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(screen.getByRole("button", { name: "Save template" })).toBeEnabled();
  });

  it("has the revisions and the instances in tabs; Upgrade refreshes the instances only", async () => {
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
    renderPage("/org/templates/t1?tab=instances");
    await user.click(await screen.findByRole("button", { name: "Upgrade" }));
    await user.click(await screen.findByRole("button", { name: "Upgrade to r3" }));
    await waitFor(() => expect(screen.queryByRole("button", { name: "Upgrade" })).not.toBeInTheDocument());
    expect(screen.getByText("r3")).toBeInTheDocument();
    // The editor read the template once (the dialog read r2 to compare
    // defaults): the upgrade didn't reload it.
    const reads = api.calls.filter((c) => c.path === "/api/v1/template/get-template");
    expect(reads.map((c) => c.query)).toEqual(["?id=t1", "?id=t1&revision=2"]);

    await user.click(screen.getByRole("tab", { name: "Revisions" }));
    expect(await screen.findByRole("button", { name: "Changes from r2" })).toBeInTheDocument();
    await user.click(screen.getByRole("tab", { name: "Editor" }));
    expect(screen.getByLabelText("Template name")).toHaveValue("twillingate");
  });
});
