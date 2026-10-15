import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import { InstancePanel } from "@/components/instance-panel";
import type { InstanceView, TemplateView } from "@/lib/templates";

const api = vi.hoisted(() => ({ updateInstance: undefined as unknown, getPublicKey: undefined as unknown }));
vi.mock("@/hooks/use-templates-api", () => ({ useTemplatesApi: () => api }));

const instance: InstanceView = {
  key: "k1",
  server_id: "s",
  app_id: "a1",
  template_id: "t1",
  template_name: "twillingate",
  revision: 2,
  name: "bakomi",
  values: { NAME: "bakomi" },
  secrets: {},
  hosts: [{ host: "bakomi.example.com", expose: "app:8080" }],
  state: "created",
  status_code: 1,
  last_error: "",
  created_at: "2026-10-12T10:00:00Z",
  updated_at: "2026-10-12T10:00:00Z",
};

const NAME = { name: "NAME", required: true, encrypted: false, default: null, services: ["app"] };
const LANG = { name: "LANG", required: false, encrypted: false, default: "en", services: ["app"] };

const current: TemplateView = {
  id: "t1",
  source: "org",
  slug: "",
  name: "twillingate",
  latest_revision: 2,
  withdrawn_at: null,
  revision: 2,
  body: { files: { "compose.yaml": "services: {}\n" }, summary: "", description: "", icon: "", color: "" },
  detected: { variables: [NAME], expose: [], backups: [], warnings: [] },
  note: "",
  created_at: "2026-10-12T10:00:00Z",
  created_by: "u1",
  revisions: null,
};

function renderPanel(props: Partial<Parameters<typeof InstancePanel>[0]> = {}) {
  return render(
    <MemoryRouter>
      <InstancePanel instance={instance} current={current} onSaved={() => {}} {...props} />
    </MemoryRouter>,
  );
}

describe("InstancePanel", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("is the note and History until a value is edited, then a sticky bar with Save & redeploy", async () => {
    const user = userEvent.setup();
    renderPanel({ onOpenHistory: vi.fn() });
    expect(screen.getByRole("button", { name: "History" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Save & redeploy" })).not.toBeInTheDocument();
    expect(screen.getByText(/detach the app from it in Settings/)).toBeInTheDocument();
    await user.type(screen.getByLabelText("NAME"), "x");
    expect(screen.getByText("Unsaved changes").closest(".sticky")).toContainElement(
      screen.getByRole("button", { name: "Save & redeploy" }),
    );
  });

  it("always offers Save & redeploy on a pending instance (a retry of its create)", () => {
    renderPanel({ instance: { ...instance, state: "pending" } });
    expect(screen.getByRole("button", { name: "Save & redeploy" })).toBeEnabled();
  });

  it("names the template and its version in plain words", () => {
    renderPanel();
    expect(screen.getByText(/Managed by template/)).toHaveTextContent("Managed by template twillingate (version 2).");
    expect(screen.getByRole("link", { name: "twillingate" })).toHaveAttribute("href", "/templates/t1");
    expect(screen.queryByText(/\br2\b/)).not.toBeInTheDocument();
    expect(screen.queryByText(/unlink/i)).not.toBeInTheDocument();
  });

  it("offers a newer version of the template", async () => {
    const user = userEvent.setup();
    const onUpgrade = vi.fn();
    renderPanel({ latestRevision: 3, onUpgrade });
    expect(screen.getByRole("status")).toHaveTextContent("A newer version of twillingate is available (version 3).");
    await user.click(screen.getByRole("button", { name: "Update" }));
    expect(onUpgrade).toHaveBeenCalled();
  });

  it("says nothing about versions when the app is on the latest, or the page can't update", () => {
    const { unmount } = renderPanel({ latestRevision: 2, onUpgrade: vi.fn() });
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
    unmount();
    renderPanel({ latestRevision: 3 });
    expect(screen.queryByRole("button", { name: "Update" })).not.toBeInTheDocument();
  });

  it("says a refused Save needs attention and focuses the first bad field", async () => {
    const user = userEvent.setup();
    const updateInstance = vi.fn();
    api.updateInstance = updateInstance;
    const withSecret: TemplateView = {
      ...current,
      detected: { ...current.detected, variables: [NAME, { name: "TOKEN", required: true, encrypted: true, default: null, services: ["app"] }] },
    };
    renderPanel({ instance: { ...instance, state: "pending", secrets: {} }, current: withSecret });
    await user.click(screen.getByRole("button", { name: "Save & redeploy" }));
    const alert = screen.getByRole("alert");
    expect(alert).toHaveTextContent("Some fields need attention.");
    // NAME is stored, TOKEN is the one a pending instance needs again.
    await waitFor(() => expect(screen.getByLabelText("TOKEN")).toHaveFocus());
    expect(updateInstance).not.toHaveBeenCalled();
    // Typing the secret clears the line.
    await user.type(screen.getByLabelText("TOKEN"), "s3cret");
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(screen.getByText("Unsaved changes")).toBeInTheDocument();
  });

  it("sends only the values that changed; a cleared one goes back to the compose file's", async () => {
    const user = userEvent.setup();
    const updateInstance = vi.fn(async () => {});
    api.updateInstance = updateInstance;
    const onSaved = vi.fn();
    renderPanel({
      instance: { ...instance, values: { NAME: "bakomi", LANG: "de" } },
      current: { ...current, detected: { ...current.detected, variables: [NAME, LANG] } },
      onSaved,
    });
    expect(screen.getByLabelText("LANG")).toHaveAttribute("placeholder", "Default: en");
    await user.clear(screen.getByLabelText("LANG"));
    await user.click(screen.getByRole("button", { name: "Save & redeploy" }));
    await waitFor(() => expect(onSaved).toHaveBeenCalled());
    expect(updateInstance).toHaveBeenCalledWith({ server_id: "s", key: "k1", values: { LANG: "" } });
  });

  it("shows the hostnames the app was created with", () => {
    renderPanel();
    expect(screen.getByText("bakomi.example.com")).toBeInTheDocument();
    expect(screen.getByText("→ app:8080")).toBeInTheDocument();
  });

  it("keeps the bar to Save and a ⋯ menu on a phone once edited", async () => {
    vi.stubGlobal("matchMedia", () => ({ matches: true, addEventListener: () => {}, removeEventListener: () => {} }));
    const user = userEvent.setup();
    const onOpenHistory = vi.fn();
    renderPanel({ onOpenHistory });
    expect(screen.getByText(/Managed by template/)).toBeInTheDocument();
    await user.type(screen.getByLabelText("NAME"), "x");
    expect(screen.queryByRole("button", { name: "History" })).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "More save options" }));
    await user.click(screen.getByRole("menuitem", { name: "History" }));
    expect(onOpenHistory).toHaveBeenCalled();
  });
});
