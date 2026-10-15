import type { ReactNode } from "react";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import { NewAppButton } from "@/components/new-app/new-app-button";
import { NewAppChoices } from "@/components/new-app/new-app-choices";
import { NotificationsContext, type NotificationsContextValue } from "@/context/notifications-context-base";
import { fakeAPI } from "@/test/fake-api";

const notifications = { connected: true, subscribe: () => () => {}, waitFor: vi.fn() } as NotificationsContextValue;
const ONE = [
  { id: "t1", source: "org", name: "twillingate", latest_revision: 2, instances: 0, behind: 0, updated_at: "" },
  // A catalog template is not the org's own: it doesn't count.
  { id: "c1", source: "catalog", name: "Actual Budget", latest_revision: 1, instances: 0, behind: 0, updated_at: "" },
];

function At() {
  const l = useLocation();
  return <p data-testid="at">{l.pathname + l.search}</p>;
}

function renderIn(ui: ReactNode) {
  return render(
    <MemoryRouter initialEntries={["/server/s1"]}>
      <NotificationsContext.Provider value={notifications}>
        <Routes>
          <Route path="*" element={<>{ui}<At /></>} />
        </Routes>
      </NotificationsContext.Provider>
    </MemoryRouter>,
  );
}

afterEach(() => vi.unstubAllGlobals());

describe("NewAppChoices", () => {
  it("offers a template only when the organization has one", () => {
    renderIn(<NewAppChoices serverId="s1" templates={1} />);
    expect(screen.getByRole("link", { name: /From a template/ })).toHaveAttribute("href", "/server/s1/new?from=template");
    expect(screen.getByRole("link", { name: /Compose file/ })).toHaveAttribute("href", "/server/s1/new?from=compose");
    expect(screen.getByRole("link", { name: /Git repository/ })).toHaveAttribute("href", "/server/s1/new?from=git");
    expect(screen.queryByTestId("template-choice-skeleton")).not.toBeInTheDocument();
    // One column on a phone, all three in a row above.
    expect(screen.getByRole("list", { name: "Ways to start" })).toHaveClass("grid", "sm:grid-cols-3");
  });

  it("leaves the template out without templates", () => {
    renderIn(<NewAppChoices serverId="s1" templates={0} />);
    expect(screen.getByRole("list", { name: "Ways to start" })).toHaveClass("sm:grid-cols-2");
    expect(screen.queryByRole("link", { name: /From a template/ })).not.toBeInTheDocument();
    expect(screen.getAllByRole("link")).toHaveLength(2);
    expect(screen.queryByTestId("template-choice-skeleton")).not.toBeInTheDocument();
  });

  it("holds the template card's slot with a skeleton while the count is unknown", () => {
    renderIn(<NewAppChoices serverId="s1" templates={null} />);
    const items = screen.getAllByRole("listitem", { hidden: true });
    // The skeleton comes first, where the template card lands.
    expect(items[0]).toBe(screen.getByTestId("template-choice-skeleton"));
    expect(screen.queryByRole("link", { name: /From a template/ })).not.toBeInTheDocument();
    expect(screen.getAllByRole("link")).toHaveLength(2);
  });
});

describe("NewAppButton", () => {
  it("opens the chooser and closes it on a pick", async () => {
    const user = userEvent.setup();
    fakeAPI({ "/api/v1/template/get-templates": { templates: ONE } });
    renderIn(<NewAppButton serverId="s1" />);
    await user.click(screen.getByRole("button", { name: "New app" }));
    const dialog = await screen.findByRole("dialog", { name: "New app" });
    expect(dialog).toHaveTextContent("How do you want to start?");
    await user.click(screen.getByRole("link", { name: /Compose file/ }));
    expect(screen.getByTestId("at")).toHaveTextContent("/server/s1/new?from=compose");
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  });

  it("shows the template card once read, and doesn't read the templates again on a second open", async () => {
    const user = userEvent.setup();
    const api = fakeAPI({ "/api/v1/template/get-templates": { templates: ONE } });
    renderIn(<NewAppButton serverId="s1" />);
    expect(api.calls).toHaveLength(0);
    await user.click(screen.getByRole("button", { name: "New app" }));
    expect(await screen.findByRole("link", { name: /From a template/ })).toBeInTheDocument();
    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    await user.click(screen.getByRole("button", { name: "New app" }));
    // Already known: no skeleton, the card is there at once.
    expect(screen.getByRole("link", { name: /From a template/ })).toBeInTheDocument();
    expect(screen.queryByTestId("template-choice-skeleton")).not.toBeInTheDocument();
    expect(api.calls).toHaveLength(1);
  });

  it("leaves the template out when the organization has only catalog templates", async () => {
    const user = userEvent.setup();
    const api = fakeAPI({ "/api/v1/template/get-templates": { templates: ONE.slice(1) } });
    renderIn(<NewAppButton serverId="s1" />);
    await user.click(screen.getByRole("button", { name: "New app" }));
    await waitFor(() => expect(api.calls).toHaveLength(1));
    await waitFor(() => expect(screen.queryByTestId("template-choice-skeleton")).not.toBeInTheDocument());
    expect(screen.queryByRole("link", { name: /From a template/ })).not.toBeInTheDocument();
  });

  it("leaves the template out when the templates can't be read", async () => {
    const user = userEvent.setup();
    const api = fakeAPI({ "/api/v1/template/get-templates": new Error("down") });
    renderIn(<NewAppButton serverId="s1" />);
    await user.click(screen.getByRole("button", { name: "New app" }));
    await waitFor(() => expect(api.calls).toHaveLength(1));
    await screen.findByRole("link", { name: /Compose file/ });
    await waitFor(() => expect(screen.queryByTestId("template-choice-skeleton")).not.toBeInTheDocument());
    expect(screen.queryByRole("link", { name: /From a template/ })).not.toBeInTheDocument();
  });
});
