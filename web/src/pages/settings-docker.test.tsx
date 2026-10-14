import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { AppsContext, type AppsContextValue } from "@/context/apps-context-base";
import type { Docker } from "@/context/use-docker";
import { DockerSettings } from "@/pages/settings-docker";

vi.mock("@/context/use-servers", () => ({ useServers: () => ({ activeServerId: "srv-1" }) }));

const toastError = vi.hoisted(() => vi.fn());
const toastSuccess = vi.hoisted(() => vi.fn());
vi.mock("sonner", () => ({ toast: { success: toastSuccess, error: toastError } }));

const setupUser = () => userEvent.setup({ delay: null });

function fakeDocker(over: Partial<Docker> = {}): Docker {
  return {
    listRegistries: vi.fn().mockResolvedValue([{ address: "ghcr.io" }]),
    createRegistry: vi.fn().mockResolvedValue(undefined),
    deleteRegistry: vi.fn().mockResolvedValue(undefined),
    listNetworks: vi.fn().mockResolvedValue([
      { id: "n1", name: "bridge", driver: "bridge", scope: "local" },
      { id: "n2", name: "proxy", driver: "bridge", scope: "local" },
    ]),
    createNetwork: vi.fn().mockResolvedValue(undefined),
    deleteNetwork: vi.fn().mockResolvedValue(undefined),
    listVolumes: vi.fn().mockResolvedValue([]),
    createVolume: vi.fn().mockResolvedValue(undefined),
    deleteVolume: vi.fn().mockResolvedValue(undefined),
    ...over,
  } as unknown as Docker;
}

function renderPage(docker: Docker) {
  const apps = { apps: [] } as unknown as AppsContextValue;
  return render(
    <MemoryRouter>
      <AppsContext.Provider value={apps}>
        <DockerSettings docker={docker} />
      </AppsContext.Provider>
    </MemoryRouter>,
  );
}

describe("DockerSettings", () => {
  beforeEach(() => {
    toastError.mockReset();
    toastSuccess.mockReset();
  });

  it("puts registries, networks and volumes on one page", async () => {
    renderPage(fakeDocker());
    expect(screen.getByRole("heading", { level: 1, name: "Docker" })).toBeInTheDocument();
    expect(screen.getByRole("region", { name: "Registries" })).toBeInTheDocument();
    expect(screen.getByRole("region", { name: "Networks" })).toBeInTheDocument();
    expect(screen.getByRole("region", { name: "Volumes" })).toBeInTheDocument();
    expect(await screen.findByText("ghcr.io")).toBeInTheDocument();
    expect(await screen.findByText("proxy")).toBeInTheDocument();
    expect(screen.getAllByText("bridge · local")).toHaveLength(2);
  });

  it("removes a registry only after a confirmation", async () => {
    const user = setupUser();
    const docker = fakeDocker();
    renderPage(docker);
    await user.click(await screen.findByRole("button", { name: "Remove registry ghcr.io" }));
    const confirm = await screen.findByRole("alertdialog");
    expect(docker.deleteRegistry).not.toHaveBeenCalled();
    await user.click(within(confirm).getByRole("button", { name: "Remove registry" }));
    expect(docker.deleteRegistry).toHaveBeenCalledWith("ghcr.io");
    await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument());
    expect(toastSuccess).toHaveBeenCalledWith("Registry removed");
    expect(docker.listRegistries).toHaveBeenCalledTimes(2);
  });

  it("offers no removal of a built-in network and keeps the confirmation open when a removal fails", async () => {
    const user = setupUser();
    const docker = fakeDocker({
      deleteNetwork: vi.fn().mockRejectedValue(new Error("network proxy has active endpoints")),
    });
    renderPage(docker);
    await screen.findByText("proxy");
    expect(screen.queryByRole("button", { name: "Remove network bridge" })).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Remove network proxy" }));
    const confirm = await screen.findByRole("alertdialog");
    await user.click(within(confirm).getByRole("button", { name: "Remove network" }));
    await waitFor(() =>
      expect(toastError).toHaveBeenCalledWith("Couldn't remove the network", {
        description: "network proxy has active endpoints",
      }),
    );
    expect(screen.getByRole("alertdialog")).toBeInTheDocument();
    expect(screen.getByText("proxy")).toBeInTheDocument();
  });

  it("adds a registry, wiping the password when the dialog closes", async () => {
    const user = setupUser();
    const docker = fakeDocker();
    renderPage(docker);
    await user.click(screen.getByRole("button", { name: /Add registry/ }));
    let dialog = await screen.findByRole("dialog");
    await user.type(within(dialog).getByLabelText("Address"), "registry.example.com");
    await user.type(within(dialog).getByLabelText("Username"), "bot");
    await user.type(within(dialog).getByLabelText("Password"), "SECRET");
    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());

    await user.click(screen.getByRole("button", { name: /Add registry/ }));
    dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByLabelText("Password")).toHaveValue("");
    const add = within(dialog).getByRole("button", { name: "Add" });
    expect(add).toBeDisabled();
    await user.type(within(dialog).getByLabelText("Address"), "registry.example.com");
    await user.type(within(dialog).getByLabelText("Username"), "bot");
    await user.type(within(dialog).getByLabelText("Password"), "pw");
    await user.click(add);
    expect(docker.createRegistry).toHaveBeenCalledWith("registry.example.com", "bot", "pw");
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  });

  it("creates a network", async () => {
    const user = setupUser();
    const docker = fakeDocker();
    renderPage(docker);
    await user.click(screen.getByRole("button", { name: /Add network/ }));
    const dialog = await screen.findByRole("dialog");
    await user.type(within(dialog).getByLabelText("Name"), " web ");
    await user.click(within(dialog).getByRole("button", { name: "Create" }));
    expect(docker.createNetwork).toHaveBeenCalledWith("web");
    await waitFor(() => expect(docker.listNetworks).toHaveBeenCalledTimes(2));
  });

  it("says so when a list is empty or can't be loaded", async () => {
    const user = setupUser();
    const listNetworks = vi.fn().mockRejectedValueOnce(new Error("agent offline")).mockResolvedValue([]);
    renderPage(fakeDocker({ listRegistries: vi.fn().mockResolvedValue([]), listNetworks }));
    expect(await screen.findByText("No registries. Add one to pull private images.")).toBeInTheDocument();
    expect(await screen.findByText("Couldn't load networks.")).toBeInTheDocument();
    expect(toastError).toHaveBeenCalledWith("Couldn't load networks", { description: "agent offline" });
    await user.click(screen.getByRole("button", { name: "Try again" }));
    expect(await screen.findByText("No networks yet.")).toBeInTheDocument();
  });
});
