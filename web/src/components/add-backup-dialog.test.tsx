import { act, cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";

import { AddBackupDialog } from "@/components/add-backup-dialog";
import type { BackupTargets } from "@/lib/backups";

const targets: BackupTargets = {
  volumes: ["data"],
  services: [
    { name: "web", mounts: [{ type: "bind", source: "./uploads", target: "/app/uploads" }] },
    { name: "worker", mounts: [] },
  ],
};

function renderDialog(t: BackupTargets | null = targets, load: () => Promise<string[]> = async () => ["disk", "offsite"]) {
  const onAdd = vi.fn();
  render(
    <MemoryRouter>
      <AddBackupDialog targets={t} taken={["database"]} open busy={false} load={load} onOpenChange={() => {}} onAdd={onAdd} />
    </MemoryRouter>,
  );
  return onAdd;
}

describe("AddBackupDialog", () => {
  it("asks for the place, the paths, the type, the storage, the schedule and the name, in that order", async () => {
    renderDialog();
    await screen.findByRole("option", { name: "disk" });
    const order = ["Where the data is", "Paths, one per line", "Type", "Storage", "Schedule", "Name"];
    const fields = order.map((l) => (l === "Type" ? screen.getByRole("radiogroup") : screen.getByLabelText(l)));
    for (let i = 1; i < fields.length; i++) {
      expect(fields[i - 1].compareDocumentPosition(fields[i]) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    }
  });

  it("adds a files backup of a service's mount, on, with the picked storage", async () => {
    const user = userEvent.setup({ delay: null });
    const onAdd = renderDialog();
    await screen.findByRole("option", { name: "disk" });
    expect(screen.getByRole("radio", { name: "Folders and files" })).toBeChecked();
    const place = screen.getByLabelText("Where the data is");
    expect(screen.getByRole("option", { name: "Volume data" })).toBeInTheDocument();
    expect(screen.queryByRole("group", { name: "Service worker" })).not.toBeInTheDocument();
    await user.selectOptions(place, "mount:web:/app/uploads");
    expect(screen.getByLabelText("Paths, one per line")).toHaveValue("/app/uploads");
    expect(screen.getByText("Paths are in the web container.")).toBeInTheDocument();
    await user.selectOptions(screen.getByLabelText("Storage"), "offsite");
    await user.selectOptions(screen.getByLabelText("Schedule"), "24h");
    await user.click(screen.getByRole("button", { name: "Add backup" }));
    expect(onAdd).toHaveBeenCalledWith({
      name: "uploads",
      type: "files",
      enabled: true,
      storage: "offsite",
      every: "24h",
      service: "web",
      paths: ["/app/uploads"],
    });
  });

  it("adds a sqlite backup in a volume", async () => {
    const user = userEvent.setup({ delay: null });
    const onAdd = renderDialog(targets, async () => ["disk"]);
    await user.click(await screen.findByRole("radio", { name: "SQLite database file" }));
    await user.selectOptions(screen.getByLabelText("Where the data is"), "volume:data");
    await user.type(screen.getByLabelText("Database file"), "audit.db");
    await user.click(screen.getByRole("button", { name: "Add backup" }));
    expect(onAdd).toHaveBeenCalledWith({ name: "audit", type: "sqlite", enabled: true, storage: "disk", every: "1h", volume: "data", path: "audit.db" });
  });

  it("preselects the only storage, and asks to pick when there are several", async () => {
    const user = userEvent.setup({ delay: null });
    renderDialog(targets, async () => ["disk"]);
    expect(await screen.findByLabelText("Storage")).toHaveValue("disk");
    cleanup();
    const onAdd = renderDialog();
    const select = await screen.findByLabelText("Storage");
    expect(select).toHaveValue("");
    await user.selectOptions(screen.getByLabelText("Where the data is"), "volume:data");
    await user.click(screen.getByRole("button", { name: "Add backup" }));
    expect(screen.getByRole("alert")).toHaveTextContent("Pick the storage the copies go to.");
    expect(onAdd).not.toHaveBeenCalled();
    await user.selectOptions(select, "disk");
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Add backup" }));
    expect(onAdd).toHaveBeenCalledWith(expect.objectContaining({ name: "data", storage: "disk", enabled: true }));
  });

  it("points to Settings → Storages and can't add when the server has no storage", async () => {
    const user = userEvent.setup({ delay: null });
    const onAdd = renderDialog(targets, async () => []);
    const link = await screen.findByRole("link", { name: "Add storage" });
    expect(link).toHaveAttribute("href", "/settings?tab=storages");
    expect(screen.queryByLabelText("Storage")).not.toBeInTheDocument();
    await user.selectOptions(screen.getByLabelText("Where the data is"), "volume:data");
    expect(screen.getByRole("button", { name: "Add backup" })).toBeDisabled();
    expect(onAdd).not.toHaveBeenCalled();
  });

  it("says when the storages can't be loaded", async () => {
    const user = userEvent.setup({ delay: null });
    const onAdd = renderDialog(targets, () => Promise.reject(new Error("server offline")));
    expect(await screen.findAllByText("server offline")).not.toHaveLength(0);
    await user.selectOptions(screen.getByLabelText("Where the data is"), "volume:data");
    await user.click(screen.getByRole("button", { name: "Add backup" }));
    expect(onAdd).not.toHaveBeenCalled();
    cleanup();
    renderDialog(targets, () => Promise.reject("x"));
    expect(await screen.findByText("The storages could not be loaded.")).toBeInTheDocument();
  });

  it("fills the name in from what was picked, until the user types their own", async () => {
    const user = userEvent.setup({ delay: null });
    const onAdd = renderDialog(targets, async () => ["disk"]);
    await screen.findByLabelText("Storage");
    const name = screen.getByLabelText("Name");
    expect(name).toHaveValue("");
    // The volume's name, then the first path's last segment.
    await user.selectOptions(screen.getByLabelText("Where the data is"), "volume:data");
    expect(name).toHaveValue("data");
    await user.type(screen.getByLabelText("Paths, one per line"), "media/Cat Pics/");
    expect(name).toHaveValue("cat-pics");
    // A mount's target starts the path, so the name follows it.
    await user.selectOptions(screen.getByLabelText("Where the data is"), "mount:web:/app/uploads");
    expect(name).toHaveValue("cat-pics");
    await user.clear(screen.getByLabelText("Paths, one per line"));
    expect(name).toHaveValue("uploads");
    // A sqlite database file loses its extension.
    await user.click(screen.getByRole("radio", { name: "SQLite database file" }));
    await user.selectOptions(screen.getByLabelText("Where the data is"), "volume:data");
    await user.type(screen.getByLabelText("Database file"), "db/Audit.sqlite3");
    expect(name).toHaveValue("audit");
    // Once the user edits it, it is theirs.
    await user.clear(name);
    await user.type(name, "My Audit");
    expect(name).toHaveValue("my audit");
    await user.clear(screen.getByLabelText("Database file"));
    await user.type(screen.getByLabelText("Database file"), "other.db");
    expect(name).toHaveValue("my audit");
    await user.click(screen.getByRole("button", { name: "Add backup" }));
    expect(screen.getByRole("alert")).toHaveTextContent("Use 1-32 lowercase letters");
    expect(onAdd).not.toHaveBeenCalled();
    await user.clear(name);
    await user.type(name, "my-audit");
    await user.click(screen.getByRole("button", { name: "Add backup" }));
    expect(onAdd).toHaveBeenCalledWith(expect.objectContaining({ name: "my-audit", path: "other.db" }));
  });

  it("names a backup after the service when the mount is the root, and avoids a taken name", async () => {
    const user = userEvent.setup({ delay: null });
    renderDialog(
      { volumes: ["database"], services: [{ name: "web", mounts: [{ type: "bind", source: "./all", target: "/" }] }] },
      async () => ["disk"],
    );
    await screen.findByLabelText("Storage");
    await user.selectOptions(screen.getByLabelText("Where the data is"), "mount:web:/");
    await user.clear(screen.getByLabelText("Paths, one per line"));
    expect(screen.getByLabelText("Name")).toHaveValue("web");
    await user.selectOptions(screen.getByLabelText("Where the data is"), "volume:database");
    expect(screen.getByLabelText("Name")).toHaveValue("database-2");
  });

  it("says what is missing once the user tries", async () => {
    const user = userEvent.setup({ delay: null });
    const onAdd = renderDialog(targets, async () => ["disk"]);
    await screen.findByLabelText("Storage");
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    await user.type(screen.getByLabelText("Name"), "database");
    await user.click(screen.getByRole("button", { name: "Add backup" }));
    expect(screen.getByRole("alert")).toHaveTextContent("Pick where the data is.");
    expect(screen.getByRole("button", { name: "Add backup" })).toBeDisabled();
    await user.selectOptions(screen.getByLabelText("Where the data is"), "volume:data");
    expect(screen.getByRole("alert")).toHaveTextContent("The app already has a backup named database.");
    await user.clear(screen.getByLabelText("Name"));
    await user.type(screen.getByLabelText("Name"), "db2");
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(onAdd).not.toHaveBeenCalled();
  });

  it("won't add before the storages are loaded", async () => {
    const user = userEvent.setup({ delay: null });
    let finish: (v: string[]) => void = () => {};
    const onAdd = renderDialog(targets, () => new Promise((resolve) => (finish = resolve)));
    await user.selectOptions(screen.getByLabelText("Where the data is"), "volume:data");
    await user.click(screen.getByRole("button", { name: "Add backup" }));
    expect(screen.getByRole("alert")).toHaveTextContent("The storages are still loading.");
    expect(onAdd).not.toHaveBeenCalled();
    await act(async () => finish(["disk"]));
    await user.click(screen.getByRole("button", { name: "Add backup" }));
    expect(onAdd).toHaveBeenCalledWith(expect.objectContaining({ name: "data", storage: "disk" }));
  });

  it("offers each volume once, and says where it is mounted", async () => {
    renderDialog({
      volumes: ["data", "cache"],
      services: [
        {
          name: "web",
          mounts: [
            { type: "volume", source: "data", target: "/data" },
            { type: "bind", source: "./uploads", target: "/app/uploads" },
          ],
        },
        { name: "worker", mounts: [{ type: "volume", source: "data", target: "/shared" }] },
      ],
    });
    expect(await screen.findByRole("option", { name: "Volume data (in web at /data, worker at /shared)" })).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "Volume cache" })).toBeInTheDocument();
    // The mount of a whole volume is not a second name for it; a bind mount is.
    expect(screen.queryByRole("option", { name: /\/data \(volume data\)/ })).not.toBeInTheDocument();
    expect(screen.queryByRole("group", { name: "Service worker" })).not.toBeInTheDocument();
    expect(screen.getByRole("option", { name: "/app/uploads (./uploads)" })).toBeInTheDocument();
    expect(screen.queryByRole("note")).not.toBeInTheDocument();
  });

  it("explains instead of asking for a place when the app has none", () => {
    renderDialog({ volumes: [], services: [{ name: "web", mounts: [] }] });
    expect(screen.getByRole("note")).toHaveTextContent(/has none/);
    expect(screen.getByRole("note")).toHaveTextContent(/web:\s+volumes:/);
    expect(screen.queryByLabelText("Where the data is")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Add backup" })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Got it" })).toBeInTheDocument();
    cleanup();
    renderDialog(null);
    expect(screen.getByRole("note")).toHaveTextContent(/app:\s+volumes:/);
  });

  it("keeps the reason an add was refused next to the button", () => {
    render(
      <MemoryRouter>
        <AddBackupDialog targets={targets} taken={[]} open busy={false} error="backups[1].volume: nope" load={async () => ["disk"]} onOpenChange={() => {}} onAdd={() => {}} />
      </MemoryRouter>,
    );
    expect(screen.getByRole("alert")).toHaveTextContent("backups[1].volume: nope");
  });
});
