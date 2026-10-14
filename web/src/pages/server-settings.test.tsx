import { render, screen } from "@testing-library/react";
import type { ReactElement } from "react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import { ServerDockerPage, ServerDomainsPage, ServerGeneralPage, ServerStoragesPage } from "@/pages/server-settings";

let ingress = true;
let hasServer = true;

vi.mock("@/context/use-servers", () => ({
  useServers: () => ({ activeServer: hasServer ? { id: "s1", name: "Alpha", features: { ingress } } : null }),
}));
vi.mock("@/context/use-docker", () => ({
  useDocker: () => ({
    hasServer,
    listRegistries: vi.fn().mockResolvedValue([]),
    listNetworks: vi.fn().mockResolvedValue([]),
    updateAgent: vi.fn(),
  }),
}));
vi.mock("@/pages/settings-domains", () => ({ DomainsCard: () => <p>domains card</p> }));
vi.mock("@/pages/settings-docker", () => ({ DockerSettings: () => <p>docker settings</p> }));
vi.mock("@/pages/settings-storages", () => ({ StoragesCard: () => <p>storages card</p> }));

function renderPage(page: ReactElement) {
  return render(
    <MemoryRouter initialEntries={["/server/s1/settings/x"]}>
      <Routes>
        <Route path="/server/s1/settings/x" element={page} />
        <Route path="/server/s1/settings/general" element={<p>general page</p>} />
      </Routes>
    </MemoryRouter>,
  );
}

describe("server settings pages", () => {
  afterEach(() => {
    ingress = true;
    hasServer = true;
  });

  it("puts the agent update on General", () => {
    renderPage(<ServerGeneralPage />);
    expect(screen.getByRole("heading", { level: 1, name: "General" })).toBeInTheDocument();
    expect(screen.getByText("Alpha")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Update agent" })).toBeInTheDocument();
  });

  it("shows Domains on a server with ingress", () => {
    renderPage(<ServerDomainsPage />);
    expect(screen.getByRole("heading", { level: 1, name: "Domains" })).toBeInTheDocument();
    expect(screen.getByText("domains card")).toBeInTheDocument();
  });

  it("opens General instead on a server without ingress", async () => {
    ingress = false;
    renderPage(<ServerDomainsPage />);
    expect(await screen.findByText("general page")).toBeInTheDocument();
  });

  it("shows Storages", () => {
    renderPage(<ServerStoragesPage />);
    expect(screen.getByRole("heading", { level: 1, name: "Storages" })).toBeInTheDocument();
    expect(screen.getByText("storages card")).toBeInTheDocument();
  });

  it("shows the Docker resources", () => {
    renderPage(<ServerDockerPage />);
    expect(screen.getByText("docker settings")).toBeInTheDocument();
  });

  it("asks for a server when there is none", () => {
    hasServer = false;
    renderPage(<ServerStoragesPage />);
    expect(screen.getByText("Select a server to manage its settings.")).toBeInTheDocument();
  });
});
