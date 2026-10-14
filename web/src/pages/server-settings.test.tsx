import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactElement } from "react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { ServerDockerPage, ServerDomainsPage, ServerGeneralPage, ServerStoragesPage } from "@/pages/server-settings";

type FakeServer = {
  id: string;
  name: string;
  lastSeenAt: string | null;
  capabilities: Record<string, string>;
  features?: Record<string, boolean>;
};

const ALPHA: FakeServer = {
  id: "s1",
  name: "Alpha",
  lastSeenAt: "2026-10-13T09:00:00Z",
  capabilities: {
    server_ip: "203.0.113.7",
    system_cpu_cores: "4",
    system_memory_total: String(16 * 1024 ** 3),
    system_disk_total: String(512 * 1024 ** 3),
    version: "1.2.3",
  },
  features: { ingress: true },
};

const state = vi.hoisted(() => ({ server: null as FakeServer | null, online: true }));
const docker = vi.hoisted(() => ({ hasServer: true, updateAgent: vi.fn() }));
const toastSuccess = vi.hoisted(() => vi.fn());
const toastError = vi.hoisted(() => vi.fn());

vi.mock("@/context/use-servers", () => ({
  useServers: () => ({
    activeServer: state.server,
    statusByServer: state.server && state.online ? { [state.server.id]: "online" } : {},
  }),
}));
vi.mock("@/context/use-docker", () => ({ useDocker: () => docker }));
vi.mock("sonner", () => ({ toast: { success: toastSuccess, error: toastError } }));
vi.mock("@/pages/settings-domains", () => ({ DomainsSettings: () => <p>domains page</p> }));

vi.mock("@/pages/settings-storages", () => ({ StoragesCard: () => <p>storages page</p> }));
vi.mock("@/pages/settings-docker", () => ({ DockerSettings: () => <p>docker page</p> }));

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
  beforeEach(() => {
    state.server = ALPHA;
    state.online = true;
    docker.hasServer = true;
    docker.updateAgent.mockReset();
    toastSuccess.mockReset();
    toastError.mockReset();
  });

  it("shows the server's facts and status on General", () => {
    renderPage(<ServerGeneralPage />);
    expect(screen.getByRole("heading", { level: 1, name: "General" })).toBeInTheDocument();
    expect(screen.getByText("Alpha")).toBeInTheDocument();
    expect(screen.getByText("Online")).toBeInTheDocument();
    expect(screen.getByText("203.0.113.7")).toBeInTheDocument();
    expect(screen.getByText("4 cores")).toBeInTheDocument();
    expect(screen.getByText("16.0 GB")).toBeInTheDocument();
    expect(screen.getByText("512.0 GB")).toBeInTheDocument();
    expect(screen.getByText("Running v1.2.3.")).toBeInTheDocument();
  });

  it("says Offline once the agent is gone, and leaves out what it never reported", () => {
    state.online = false;
    state.server = { ...ALPHA, capabilities: {} };
    renderPage(<ServerGeneralPage />);
    expect(screen.getByText("Offline")).toBeInTheDocument();
    expect(screen.queryByText("IP address")).not.toBeInTheDocument();
    expect(screen.getByText("The agent hasn't reported its version yet.")).toBeInTheDocument();
  });

  it("updates the agent to a typed version", async () => {
    const user = userEvent.setup({ delay: null });
    docker.updateAgent.mockResolvedValue(undefined);
    renderPage(<ServerGeneralPage />);
    const update = screen.getByRole("button", { name: "Update" });
    expect(update).toBeDisabled();
    await user.type(screen.getByLabelText("Version"), " 1.2.4 ");
    await user.click(update);
    expect(docker.updateAgent).toHaveBeenCalledWith("1.2.4");
    await waitFor(() =>
      expect(toastSuccess).toHaveBeenCalledWith("Update started", {
        description: "The agent restarts on the new version in a moment.",
      }),
    );
    expect(screen.getByLabelText("Version")).toHaveValue("");
  });

  it("keeps the version when the update is refused", async () => {
    const user = userEvent.setup({ delay: null });
    docker.updateAgent.mockRejectedValue(new Error("a restore is running"));
    renderPage(<ServerGeneralPage />);
    await user.type(screen.getByLabelText("Version"), "1.2.4");
    await user.click(screen.getByRole("button", { name: "Update" }));
    await waitFor(() =>
      expect(toastError).toHaveBeenCalledWith("Couldn't update the agent", { description: "a restore is running" }),
    );
    expect(screen.getByLabelText("Version")).toHaveValue("1.2.4");
  });

  it("starts the update field over on another server", async () => {
    const user = userEvent.setup({ delay: null });
    const view = renderPage(<ServerGeneralPage />);
    await user.type(screen.getByLabelText("Version"), "1.2.4");
    state.server = { ...ALPHA, id: "s2", name: "Beta" };
    view.rerender(
      <MemoryRouter initialEntries={["/server/s1/settings/x"]}>
        <Routes>
          <Route path="/server/s1/settings/x" element={<ServerGeneralPage />} />
        </Routes>
      </MemoryRouter>,
    );
    expect(screen.getByLabelText("Version")).toHaveValue("");
  });

  it("shows Domains on a server with ingress", () => {
    renderPage(<ServerDomainsPage />);
    expect(screen.getByText("domains page")).toBeInTheDocument();
  });

  it("opens General instead of Domains on a server without ingress", async () => {
    state.server = { ...ALPHA, features: {} };
    renderPage(<ServerDomainsPage />);
    expect(await screen.findByText("general page")).toBeInTheDocument();
  });

  it("shows Storages", () => {
    renderPage(<ServerStoragesPage />);
    expect(screen.getByText("storages page")).toBeInTheDocument();
  });

  it("shows the Docker resources", () => {
    renderPage(<ServerDockerPage />);
    expect(screen.getByText("docker page")).toBeInTheDocument();
  });

  it("asks for a server when there is none", () => {
    docker.hasServer = false;
    state.server = null;
    renderPage(<ServerGeneralPage />);
    expect(screen.getByText("Select a server to manage its settings.")).toBeInTheDocument();
  });
});
