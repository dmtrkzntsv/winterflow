import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import { InstancePanel } from "@/components/instance-panel";
import type { InstanceView, TemplateView } from "@/lib/templates";

vi.mock("@/hooks/use-templates-api", () => ({ useTemplatesApi: () => ({}) }));

const instance: InstanceView = {
  key: "k1",
  server_id: "s",
  app_id: "a1",
  template_id: "t1",
  template_name: "twillingate",
  revision: 2,
  parameters: { NAME: "bakomi" },
  secrets: {},
  state: "created",
  status_code: 1,
  last_error: "",
  created_at: "2026-10-12T10:00:00Z",
  updated_at: "2026-10-12T10:00:00Z",
};

const current: TemplateView = {
  id: "t1",
  name: "twillingate",
  latest_revision: 2,
  revision: 2,
  body: {
    files: { "compose.yaml": "services: {}\n" },
    parameters: [{ name: "NAME", required: true }],
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

  it("puts History in its bar, before Save", () => {
    renderPanel({ onOpenHistory: vi.fn() });
    const history = screen.getByRole("button", { name: "History" });
    const save = screen.getByRole("button", { name: "Save & redeploy" });
    expect(history.compareDocumentPosition(save) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(screen.getByText(/detach the app from it in Settings/)).toBeInTheDocument();
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

  it("keeps the bar to Save and a ⋯ menu on a phone, with the note under it", async () => {
    vi.stubGlobal("matchMedia", () => ({ matches: true, addEventListener: () => {}, removeEventListener: () => {} }));
    const user = userEvent.setup();
    const onOpenHistory = vi.fn();
    renderPanel({ onOpenHistory });
    expect(screen.queryByRole("button", { name: "History" })).not.toBeInTheDocument();
    expect(screen.getByText(/Managed by template/)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "More save options" }));
    await user.click(screen.getByRole("menuitem", { name: "History" }));
    expect(onOpenHistory).toHaveBeenCalled();
  });
});
