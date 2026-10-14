import { render, screen, within } from "@testing-library/react";
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

afterEach(() => {
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

describe("OrgTemplatesPage", () => {
  it("shows each template as a card: how many apps use it and when it changed", async () => {
    vi.useFakeTimers({ toFake: ["Date"] });
    vi.setSystemTime(new Date("2026-10-14T10:00:00Z"));
    const user = userEvent.setup();
    fakeAPI({
      "/api/v1/template/get-templates": {
        templates: [{ id: "t1", name: "twillingate", latest_revision: 5, instances: 12, behind: 3, updated_at: "2026-10-12T10:00:00Z" }],
      },
    });
    renderPage();
    expect(screen.getByRole("heading", { level: 1, name: "Templates" })).toBeInTheDocument();
    const card = await screen.findByRole("link", { name: /twillingate/ });
    expect(card).toHaveAttribute("href", "/templates/t1");
    expect(within(card).getByText("Used by 12 apps · 3 on an older version")).toBeInTheDocument();
    expect(within(card).getByText("Updated 2 days ago")).toBeInTheDocument();
    expect(screen.queryByText("r5")).not.toBeInTheDocument();
    await user.click(card);
    expect(await screen.findByText("template page")).toBeInTheDocument();
  });

  it("offers New template as a link, and says what to do when there is none", async () => {
    const user = userEvent.setup();
    fakeAPI({ "/api/v1/template/get-templates": { templates: null } });
    renderPage();
    expect(await screen.findByText(/No templates yet/)).toBeInTheDocument();
    const add = screen.getByRole("link", { name: /New template/ });
    expect(add).toHaveAttribute("href", "/templates/new");
    await user.click(add);
    expect(await screen.findByText("new template")).toBeInTheDocument();
  });

  it("shows why the list failed", async () => {
    fakeAPI({ "/api/v1/template/get-templates": new Error("database down") });
    renderPage();
    expect(await screen.findByRole("alert")).toHaveTextContent("database down");
  });
});
