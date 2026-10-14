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
const ONE = [{ id: "t1", name: "twillingate", latest_revision: 2, instances: 0, behind: 0, updated_at: "" }];

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
  it("offers a template only when the organization has one", async () => {
    fakeAPI({ "/api/v1/template/get-templates": { templates: ONE } });
    renderIn(<NewAppChoices serverId="s1" />);
    expect(await screen.findByRole("link", { name: /From a template/ })).toHaveAttribute("href", "/server/s1/new?from=template");
    expect(screen.getByRole("link", { name: /Compose file/ })).toHaveAttribute("href", "/server/s1/new?from=compose");
    expect(screen.getByRole("link", { name: /Git repository/ })).toHaveAttribute("href", "/server/s1/new?from=git");
  });

  it("leaves the template out without templates, or when they can't be read", async () => {
    const api = fakeAPI({ "/api/v1/template/get-templates": { templates: [] } });
    const { unmount } = renderIn(<NewAppChoices serverId="s1" />);
    await waitFor(() => expect(api.calls).toHaveLength(1));
    expect(screen.queryByRole("link", { name: /From a template/ })).not.toBeInTheDocument();
    expect(screen.getAllByRole("link")).toHaveLength(2);
    unmount();
    vi.unstubAllGlobals();

    const failing = fakeAPI({ "/api/v1/template/get-templates": new Error("down") });
    renderIn(<NewAppChoices serverId="s1" />);
    await waitFor(() => expect(failing.calls).toHaveLength(1));
    expect(screen.queryByRole("link", { name: /From a template/ })).not.toBeInTheDocument();
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
});
