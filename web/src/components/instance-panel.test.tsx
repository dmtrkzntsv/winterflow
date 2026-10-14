import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";

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

describe("InstancePanel", () => {
  it("puts the page's actions in its bar, before Save", () => {
    render(
      <MemoryRouter>
        <InstancePanel
          instance={instance}
          current={current}
          onSaved={() => {}}
          headerActions={<button type="button">History</button>}
        />
      </MemoryRouter>,
    );
    const history = screen.getByRole("button", { name: "History" });
    const save = screen.getByRole("button", { name: "Save & redeploy" });
    expect(history.compareDocumentPosition(save) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(screen.getByText(/detach the app from the template in Settings/)).toBeInTheDocument();
  });
});
