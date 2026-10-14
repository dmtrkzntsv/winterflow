import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import { NotificationsContext, type NotificationsContextValue } from "@/context/notifications-context-base";
import OrgTemplatesPage from "@/pages/org-templates";
import { fakeAPI } from "@/test/fake-api";


const notifications = { connected: true, subscribe: () => () => {}, waitFor: vi.fn() } as NotificationsContextValue;

function renderPage() {
  return render(
    <MemoryRouter initialEntries={["/templates"]}>
      <NotificationsContext.Provider value={notifications}>
        <Routes>
          <Route path="/templates" element={<OrgTemplatesPage />} />
          <Route path="/templates/new" element={<p>new template</p>} />
          <Route path="/templates/:id" element={<p>template page</p>} />
        </Routes>
      </NotificationsContext.Provider>
    </MemoryRouter>,
  );
}

afterEach(() => vi.unstubAllGlobals());

describe("OrgTemplatesPage", () => {
  it("lists templates with their revision and instances", async () => {
    const user = userEvent.setup();
    fakeAPI({
      "/api/v1/template/get-templates": {
        templates: [{ id: "t1", name: "twillingate", latest_revision: 5, instances: 12, behind: 3, updated_at: "2026-10-12T10:00:00Z" }],
      },
    });
    renderPage();
    expect(await screen.findByText("12 instances · 3 behind")).toBeInTheDocument();
    expect(screen.getByText("r5")).toBeInTheDocument();
    await user.click(screen.getByRole("link", { name: "twillingate" }));
    expect(await screen.findByText("template page")).toBeInTheDocument();
  });

  it("offers Add template, also when there is none", async () => {
    const user = userEvent.setup();
    fakeAPI({ "/api/v1/template/get-templates": { templates: null } });
    renderPage();
    expect(await screen.findByText(/No templates yet/)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /Add template/ }));
    expect(await screen.findByText("new template")).toBeInTheDocument();
  });

  it("shows why the list failed", async () => {
    fakeAPI({ "/api/v1/template/get-templates": new Error("database down") });
    renderPage();
    expect(await screen.findByRole("alert")).toHaveTextContent("database down");
  });
});
