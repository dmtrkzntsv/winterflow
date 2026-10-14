import { render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import { NotificationsContext, type NotificationsContextValue } from "@/context/notifications-context-base";
import NewAppPage from "@/pages/new-app";
import { fakeAPI } from "@/test/fake-api";

vi.mock("@/context/use-servers", () => ({ useServers: () => ({ activeServerId: "s1" }) }));
vi.mock("@/components/new-app/compose-flow", () => ({ ComposeFlow: ({ serverId }: { serverId: string }) => <p>compose form {serverId}</p> }));
vi.mock("@/components/new-app/git-flow", () => ({ GitFlow: ({ serverId }: { serverId: string }) => <p>git form {serverId}</p> }));
vi.mock("@/components/new-app/template-flow", () => ({
  TemplateFlow: ({ serverId }: { serverId: string }) => <p>template form {serverId}</p>,
}));

const notifications = { connected: true, subscribe: () => () => {}, waitFor: vi.fn() } as NotificationsContextValue;

function renderAt(url: string) {
  fakeAPI({ "/api/v1/template/get-templates": { templates: [] } });
  return render(
    <MemoryRouter initialEntries={[url]}>
      <NotificationsContext.Provider value={notifications}>
        <Routes>
          <Route path="/server/s1/new" element={<NewAppPage />} />
        </Routes>
      </NotificationsContext.Provider>
    </MemoryRouter>,
  );
}

afterEach(() => vi.unstubAllGlobals());

describe("NewAppPage", () => {
  it.each(["/server/s1/new", "/server/s1/new?from=nonsense"])("shows the chooser at %s", async (url) => {
    renderAt(url);
    expect(screen.getByRole("heading", { level: 1, name: "New app" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Apps" })).toHaveAttribute("href", "/server/s1");
    expect(await screen.findByRole("link", { name: /Compose file/ })).toBeInTheDocument();
    expect(document.title).toContain("New app");
  });

  it.each([
    ["compose", "New app from a compose file", "compose form s1"],
    ["git", "New app from Git", "git form s1"],
    ["template", "New app from a template", "template form s1"],
  ])("opens the %s form", (from, title, body) => {
    renderAt(`/server/s1/new?from=${from}`);
    expect(screen.getByRole("heading", { level: 1, name: title })).toBeInTheDocument();
    expect(screen.getByText(body)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "New app" })).toHaveAttribute("href", "/server/s1/new");
  });
});
