import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
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

function renderDialog(t: BackupTargets | null = targets) {
  const onAdd = vi.fn();
  render(<AddBackupDialog targets={t} taken={["database"]} open busy={false} onOpenChange={() => {}} onAdd={onAdd} />);
  return onAdd;
}

describe("AddBackupDialog", () => {
  it("adds a files backup of a service's mount", async () => {
    const user = userEvent.setup({ delay: null });
    const onAdd = renderDialog();
    await user.type(screen.getByLabelText("Name"), "Uploads");
    await user.click(screen.getByRole("radio", { name: "Folders and files" }));
    const place = screen.getByLabelText("Where the data is");
    expect(screen.getByRole("option", { name: "Volume data" })).toBeInTheDocument();
    expect(screen.queryByRole("group", { name: "Service worker" })).not.toBeInTheDocument();
    await user.selectOptions(place, "mount:web:/app/uploads");
    expect(screen.getByLabelText("Paths, one per line")).toHaveValue("/app/uploads");
    expect(screen.getByText("Paths are in the web container.")).toBeInTheDocument();
    await user.selectOptions(screen.getByLabelText("Schedule"), "24h");
    await user.click(screen.getByRole("button", { name: "Add backup" }));
    expect(onAdd).toHaveBeenCalledWith({ name: "uploads", type: "files", every: "24h", service: "web", paths: ["/app/uploads"] });
  });

  it("adds a sqlite backup in a volume", async () => {
    const user = userEvent.setup({ delay: null });
    const onAdd = renderDialog();
    await user.type(screen.getByLabelText("Name"), "audit");
    await user.selectOptions(screen.getByLabelText("Where the data is"), "volume:data");
    await user.type(screen.getByLabelText("Database file"), "audit.db");
    await user.click(screen.getByRole("button", { name: "Add backup" }));
    expect(onAdd).toHaveBeenCalledWith({ name: "audit", type: "sqlite", every: "1h", volume: "data", path: "audit.db" });
  });

  it("says what is missing once the user tries", async () => {
    const user = userEvent.setup({ delay: null });
    const onAdd = renderDialog(null);
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    await user.type(screen.getByLabelText("Name"), "database");
    await user.click(screen.getByRole("button", { name: "Add backup" }));
    expect(screen.getByRole("alert")).toHaveTextContent("The app already has a backup named database.");
    expect(screen.getByRole("button", { name: "Add backup" })).toBeDisabled();
    await user.clear(screen.getByLabelText("Name"));
    await user.type(screen.getByLabelText("Name"), "db2");
    expect(screen.getByRole("alert")).toHaveTextContent("Pick where the data is.");
    expect(onAdd).not.toHaveBeenCalled();
  });
});
