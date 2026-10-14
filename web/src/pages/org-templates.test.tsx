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
  it("draws each card with its template's icon and colour", async () => {
    fakeAPI({
      "/api/v1/template/get-templates": {
        templates: [
          { id: "t1", name: "notes", latest_revision: 2, instances: 0, behind: 0, updated_at: "2026-10-12T10:00:00Z" },
          { id: "t2", name: "plain", latest_revision: 1, instances: 0, behind: 0, updated_at: "2026-10-12T10:00:00Z" },
        ],
      },
      "/api/v1/template/get-template": (_: unknown, q: URLSearchParams) =>
        q.get("id") === "t1"
          ? { id: "t1", name: "notes", revision: 2, latest_revision: 2, body: { icon: "book", color: "#0d9488" } }
          : new Error("gone"),
    });
    renderPage();
    const notes = await screen.findByRole("link", { name: /notes/ });
    const tile = () => notes.querySelector("div[style]") as HTMLElement;
    await vi.waitFor(() => expect(tile().style.backgroundColor).toBe("rgb(13, 148, 136)"));
    expect(tile().querySelector("svg")).not.toBeNull();
    // A template whose look can't be read keeps its letter.
    expect(within(screen.getByRole("link", { name: /plain/ })).getByText("P")).toBeInTheDocument();
  });

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
