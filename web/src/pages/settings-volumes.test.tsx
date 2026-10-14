import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";

import { AppsContext, type AppsContextValue } from "@/context/apps-context-base";
import type { Docker, Volume } from "@/context/use-docker";
import { VolumesSection } from "@/pages/settings-volumes";

// Links point into the active server's scope (/server/:id/...).
vi.mock("@/context/use-servers", () => ({ useServers: () => ({ activeServerId: "srv-1" }) }));

const toastError = vi.hoisted(() => vi.fn());
vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: toastError } }));

const HASH = "fedcba9876543210".repeat(4);

const VOLUMES: Volume[] = [
  { name: "wf-a1_data", app_id: "a1", project: "wf-a1", in_use: true, orphan: false, anonymous: false },
  { name: "wf-gone_db", app_id: "gone", project: "wf-gone", in_use: false, orphan: true, anonymous: false },
  { name: "shared", in_use: false, orphan: false, anonymous: false },
  { name: HASH, in_use: false, orphan: false, anonymous: true },
];

// fakeDocker stubs only what VolumesSection calls.
function fakeDocker(over: Partial<Docker> = {}): Docker {
  return {
    listVolumes: vi.fn().mockResolvedValue(VOLUMES),
    createVolume: vi.fn().mockResolvedValue(undefined),
    deleteVolume: vi.fn().mockResolvedValue(undefined),
    ...over,
  } as unknown as Docker;
}

function renderSection(docker: Docker) {
  const apps = { apps: [{ id: "a1", name: "Nextcloud" }] } as unknown as AppsContextValue;
  return render(
    <MemoryRouter>
      <AppsContext.Provider value={apps}>
        <VolumesSection docker={docker} />
      </AppsContext.Provider>
    </MemoryRouter>,
  );
}

describe("VolumesSection", () => {
  it("lists named volumes grouped, with owners and how many no app uses", async () => {
    renderSection(fakeDocker());
    expect(await screen.findByText("Winterflow apps")).toBeInTheDocument();
    expect(screen.getByText("Other volumes")).toBeInTheDocument();
    expect(screen.queryByText("Anonymous")).not.toBeInTheDocument();
    // Short names, full name kept in the title.
    expect(screen.getByTitle("wf-a1_data")).toHaveTextContent("data");
    expect(screen.getByTitle("shared")).toHaveTextContent("shared");
    expect(screen.getByRole("link", { name: "Nextcloud" })).toHaveAttribute("href", "/server/srv-1/app/a1");
    expect(screen.getByText("In use")).toBeInTheDocument();
    expect(screen.getByText("Not used by any app")).toBeInTheDocument();
    expect(screen.getByText("App not on this server")).toHaveAttribute("title", "No app gone on this server");
    expect(screen.getByText(/3 volumes/)).toBeInTheDocument();
    expect(screen.getByText(/1 not used by any app/)).toBeInTheDocument();
    expect(screen.queryByText(/orphan/i)).not.toBeInTheDocument();
  });

  it("shows anonymous volumes only when switched on", async () => {
    const user = userEvent.setup({ delay: null });
    renderSection(fakeDocker());
    await user.click(await screen.findByRole("switch", { name: "Show 1 anonymous" }));
    expect(screen.getByText("Anonymous")).toBeInTheDocument();
    expect(screen.getByTitle(HASH)).toHaveTextContent(`${HASH.slice(0, 12)}…`);
    expect(screen.getByText(/4 volumes/)).toBeInTheDocument();
  });

  it("deletes an unused volume after confirmation and reloads", async () => {
    const user = userEvent.setup({ delay: null });
    const docker = fakeDocker({
      listVolumes: vi.fn().mockResolvedValueOnce(VOLUMES).mockResolvedValue(VOLUMES.filter((v) => v.name !== "shared")),
    });
    renderSection(docker);
    await screen.findByText("Winterflow apps");
    // A mounted volume offers no delete, every other one does.
    expect(screen.queryByRole("button", { name: "Delete volume wf-a1_data" })).not.toBeInTheDocument();
    expect(screen.getAllByRole("button", { name: /^Delete volume/ })).toHaveLength(2);
    await user.click(screen.getByRole("button", { name: "Delete volume shared" }));
    const dialog = await screen.findByRole("alertdialog");
    expect(dialog).toHaveTextContent("shared and all the data in it are erased for good.");
    await user.click(within(dialog).getByRole("button", { name: "Delete volume" }));
    expect(docker.deleteVolume).toHaveBeenCalledWith("shared");
    await waitFor(() => expect(docker.listVolumes).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument());
    // The row is gone with its button: focus lands on Create volume, not <body>.
    await waitFor(() => expect(screen.getByRole("button", { name: /Create volume/ })).toHaveFocus());
  });

  it("keeps the confirmation open and lists again when Docker refuses", async () => {
    const user = userEvent.setup({ delay: null });
    const docker = fakeDocker({ deleteVolume: vi.fn().mockRejectedValue(new Error("volume is in use")) });
    renderSection(docker);
    await user.click(await screen.findByRole("button", { name: "Delete volume shared" }));
    await user.click(within(await screen.findByRole("alertdialog")).getByRole("button", { name: "Delete volume" }));
    await waitFor(() => expect(toastError).toHaveBeenCalledWith("Couldn't delete the volume", { description: "volume is in use" }));
    expect(docker.listVolumes).toHaveBeenCalledTimes(2);
    expect(screen.getByRole("alertdialog")).toBeInTheDocument();
    // Cancelling after a refusal puts focus back on what opened it.
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.getByRole("button", { name: "Delete volume shared" })).toHaveFocus());
  });

  it("validates the name before creating", async () => {
    const user = userEvent.setup({ delay: null });
    const docker = fakeDocker();
    renderSection(docker);
    await user.click(await screen.findByRole("button", { name: /Create volume/ }));
    const dialog = await screen.findByRole("dialog");
    const create = within(dialog).getByRole("button", { name: "Create" });
    expect(create).toBeDisabled();
    await user.type(within(dialog).getByLabelText("Name"), "-bad");
    expect(within(dialog).getByText(/Start with a letter or digit/)).toBeInTheDocument();
    expect(create).toBeDisabled();
    await user.clear(within(dialog).getByLabelText("Name"));
    await user.type(within(dialog).getByLabelText("Name"), "media");
    await user.click(create);
    expect(docker.createVolume).toHaveBeenCalledWith("media");
  });

  it("says so when the list can't be loaded", async () => {
    renderSection(fakeDocker({ listVolumes: vi.fn().mockRejectedValue(new Error("agent offline")) }));
    expect(await screen.findByText("Couldn't load volumes.")).toBeInTheDocument();
  });
});
