import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import { NotificationsContext, type NotificationsContextValue } from "@/context/notifications-context-base";
import OrgTemplatesPage from "@/pages/org-templates";
import { fakeAPI } from "@/test/fake-api";
import { rollout } from "@/test/rollouts";

type Listener = Parameters<NotificationsContextValue["subscribe"]>[0];
const listeners = new Set<Listener>();
const notifications = { connected: true, subscribe: (fn: Listener) => {
  listeners.add(fn);
  return () => { listeners.delete(fn); };
}, waitFor: vi.fn() } as NotificationsContextValue;

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
          { id: "t1", source: "org", name: "notes", latest_revision: 2, instances: 0, behind: 0, updated_at: "2026-10-12T10:00:00Z" },
          { id: "t2", source: "org", name: "plain", latest_revision: 1, instances: 0, behind: 0, updated_at: "2026-10-12T10:00:00Z" },
        ],
      },
      "/api/v1/template/get-template": (_: unknown, q: URLSearchParams) =>
        q.get("id") === "t1"
          ? { id: "t1", source: "org", name: "notes", revision: 2, latest_revision: 2, body: { icon: "book", color: "#0d9488" } }
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
        templates: [{ id: "t1", source: "org", name: "twillingate", latest_revision: 5, instances: 12, behind: 3, updated_at: "2026-10-12T10:00:00Z" }],
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

  it("shows org templates before the catalog, with used catalog entries before its grid", async () => {
    fakeAPI({
      "/api/v1/template/get-templates": {
        templates: [
          { id: "t1", source: "org", name: "mine", latest_revision: 1, instances: 0, behind: 0, updated_at: "2026-10-12T10:00:00Z" },
          { id: "c1", source: "catalog", slug: "actual", name: "Actual Budget", latest_revision: 3, instances: 2, behind: 1, updated_at: "2026-10-12T10:00:00Z", catalog: { summary: "Private finances" } },
          { id: "c2", source: "catalog", slug: "blog", name: "Blog", latest_revision: 1, instances: 0, behind: 0, updated_at: "2026-10-12T10:00:00Z", catalog: { summary: "Publish writing" } },
        ],
      },
    });
    renderPage();
    expect(await screen.findByRole("link", { name: /mine/ })).toBeInTheDocument();
    const org = screen.getByRole("list", { name: "Your templates" });
    const used = screen.getByRole("list", { name: "Catalog templates in use" });
    const grid = screen.getByRole("list", { name: "Catalog templates" });
    expect(org.compareDocumentPosition(used) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(used.compareDocumentPosition(grid) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(within(used).getByRole("link", { name: "Actual Budget" })).toBeInTheDocument();
    expect(within(used).getByRole("link", { name: "Actual Budget" })).toHaveClass("absolute", "inset-0");
    expect(within(used).getByRole("link", { name: "Actual Budget" }).closest("li")).toContainElement(within(used).getByText("Private finances"));
    expect(within(used).getByText("Used by 2 apps · 1 on an older version")).toBeInTheDocument();
    expect(within(used).getByText("Upgrade available")).toBeInTheDocument();
    expect(within(grid).queryByText("Actual Budget")).not.toBeInTheDocument();
    expect(within(grid).getByRole("link", { name: "Blog" })).toBeInTheDocument();
    expect(within(grid).getByRole("link", { name: "Blog" })).toHaveClass("absolute", "inset-0");
  });

  it("searches the remaining catalog by current summary, keyword and alternative", async () => {
    const user = userEvent.setup();
    fakeAPI({
      "/api/v1/template/get-templates": {
        templates: [
          { id: "c1", source: "catalog", name: "Actual", latest_revision: 1, instances: 0, behind: 0, withdrawn_at: null,
            catalog: { summary: "Private finances", keywords: ["budget"], alternative_to: ["YNAB"] } },
          { id: "c2", source: "catalog", name: "Ghost", latest_revision: 2, instances: 1, behind: 0, withdrawn_at: "2026-10-15",
            catalog: { summary: "Old" } },
        ],
      },
    });
    renderPage();
    expect(await screen.findByRole("link", { name: "Actual" })).toBeInTheDocument();
    expect(screen.getByText("Withdrawn from the catalog")).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "Create app" })).not.toBeInTheDocument();
    const search = screen.getByRole("textbox", { name: "Search catalog" });
    for (const term of ["finances", "budget", "ynab"]) {
      await user.clear(search);
      await user.type(search, term);
      expect(within(screen.getByRole("list", { name: "Catalog templates" })).getByRole("link", { name: "Actual" })).toBeInTheDocument();
    }
    await user.clear(search);
    await user.type(search, "unmatched");
    expect(screen.getByText("No catalog templates match your search.")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Ghost" })).toBeInTheDocument();
  });

  it("confirms Upgrade all and refreshes used counts as rollout progresses", async () => {
    const user = userEvent.setup();
    let behind = 2;
    const api = fakeAPI({
      "/api/v1/template/get-templates": () => ({ templates: [{ id: "c1", source: "catalog", name: "Actual", latest_revision: 3, instances: 2, behind, withdrawn_at: null }] }),
      "/api/v1/rollout/get-rollouts": { rollouts: [] },
      "/api/v1/template/upgrade-instances": rollout({ scope_key: "c1", revision: 3, state: "queued" }),
    });
    renderPage();
    await user.click(await screen.findByRole("button", { name: "Upgrade all" }));
    expect(screen.getByRole("alertdialog")).toHaveTextContent("Upgrade 2 apps to version 3?");
    await user.click(within(screen.getByRole("alertdialog")).getByRole("button", { name: "Upgrade all" }));
    expect(api.posted("/api/v1/template/upgrade-instances")).toEqual([{ id: "c1", revision: 3 }]);
    await waitFor(() => expect(screen.getByRole("button", { name: "Upgrade all" })).toBeDisabled());
    behind = 1;
    act(() => {
      for (const fn of listeners) fn({ type: "rollout", payload: rollout({ scope_key: "c1", revision: 3, done: 1, state: "running" }) } as Parameters<Listener>[0]);
    });
    await waitFor(() => expect(api.calls.filter((c) => c.path === "/api/v1/template/get-templates")).toHaveLength(2));
    expect(screen.getByText("Used by 2 apps · 1 on an older version")).toBeInTheDocument();
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
