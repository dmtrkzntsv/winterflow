import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";

import { AppsContext, type AppsContextValue } from "@/context/apps-context-base";
import type { Docker, Volume } from "@/context/use-docker";
import { VolumesCard } from "@/pages/settings-volumes";

const HASH = "fedcba9876543210".repeat(4);

const VOLUMES: Volume[] = [
  { name: "wf-a1_data", app_id: "a1", project: "wf-a1", in_use: true, orphan: false, anonymous: false },
  { name: "wf-gone_db", app_id: "gone", project: "wf-gone", in_use: false, orphan: true, anonymous: false },
  { name: "shared", in_use: false, orphan: false, anonymous: false },
  { name: HASH, in_use: false, orphan: false, anonymous: true },
];

// fakeDocker stubs only what VolumesCard calls; the rest of Docker is unused.
function fakeDocker(over: Partial<Docker> = {}): Docker {
  return {
    listVolumes: vi.fn().mockResolvedValue(VOLUMES),
    createVolume: vi.fn().mockResolvedValue(undefined),
    deleteVolume: vi.fn().mockResolvedValue(undefined),
    ...over,
  } as unknown as Docker;
}

// No ProfileContext: the card no longer depends on who is looking.
function renderCard(docker: Docker) {
  const apps = {
    apps: [{ id: "a1", name: "Nextcloud" }],
  } as unknown as AppsContextValue;
  return render(
    <MemoryRouter>
      <AppsContext.Provider value={apps}>
        <VolumesCard docker={docker} />
      </AppsContext.Provider>
    </MemoryRouter>,
  );
}

describe("VolumesCard", () => {
  it("lists named volumes grouped, with owners and the orphan count", async () => {
    renderCard(fakeDocker());

    expect(await screen.findByText("Winterflow apps")).toBeInTheDocument();
    expect(screen.getByText("Other volumes")).toBeInTheDocument();
    expect(screen.queryByText("Anonymous")).not.toBeInTheDocument();

    // Short names, full name kept in the title.
    expect(screen.getByTitle("wf-a1_data")).toHaveTextContent("data");
    expect(screen.getByTitle("shared")).toHaveTextContent("shared");
    // The owning app links to its page (once per layout: desktop + phone).
    expect(screen.getAllByRole("link", { name: "Nextcloud" })[0]).toHaveAttribute(
      "href",
      "/app/a1",
    );
    expect(screen.getByText(/3 volumes/)).toBeInTheDocument();
    expect(screen.getByText(/1 orphaned/)).toBeInTheDocument();
  });

  it("shows anonymous volumes only when switched on", async () => {
    const user = userEvent.setup();
    renderCard(fakeDocker());

    await user.click(await screen.findByRole("switch"));

    expect(screen.getByText("Anonymous")).toBeInTheDocument();
    expect(screen.getByTitle(HASH)).toHaveTextContent(`${HASH.slice(0, 12)}…`);
    expect(screen.getByText(/4 volumes/)).toBeInTheDocument();
  });

  it("deletes an unused volume after confirmation and reloads", async () => {
    const user = userEvent.setup();
    const docker = fakeDocker();
    renderCard(docker);

    // A mounted volume offers no delete button.
    await screen.findByText("Winterflow apps");
    expect(
      screen.queryByRole("button", { name: "Delete volume wf-a1_data" }),
    ).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Delete volume shared" }));
    const dialog = await screen.findByRole("alertdialog");
    await user.click(within(dialog).getByRole("button", { name: "Delete volume" }));

    expect(docker.deleteVolume).toHaveBeenCalledWith("shared");
    expect(docker.listVolumes).toHaveBeenCalledTimes(2);
  });

  it("offers create and delete to every user", async () => {
    renderCard(fakeDocker());

    await screen.findByText("Winterflow apps");
    expect(screen.getByRole("button", { name: /Create volume/ })).toBeInTheDocument();
    // Every unused volume is deletable (the mounted wf-a1_data is not).
    expect(screen.getAllByRole("button", { name: /^Delete volume/ })).toHaveLength(2);
    // The group header spans every column, actions included.
    expect(screen.getByText("Winterflow apps").closest("td")).toHaveAttribute("colspan", "4");
  });

  it("validates the name before creating", async () => {
    const user = userEvent.setup();
    const docker = fakeDocker();
    renderCard(docker);

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
    renderCard(
      fakeDocker({ listVolumes: vi.fn().mockRejectedValue(new Error("agent offline")) }),
    );

    expect(await screen.findByText("Couldn't load volumes.")).toBeInTheDocument();
  });
});
