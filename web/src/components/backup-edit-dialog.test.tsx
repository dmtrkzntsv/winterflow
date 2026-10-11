import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { BackupEditDialog } from "@/components/backup-edit-dialog";
import type { BackupTargets, EffectiveBackup } from "@/lib/backups";

const all = { every: true, keep: true, volume: true, service: true, path: true, paths: true, exclude: true };
const targets: BackupTargets = {
  volumes: ["data", "media"],
  services: [{ name: "web", mounts: [{ type: "volume", source: "data", target: "/data" }] }],
};
const database: EffectiveBackup = {
  name: "database",
  type: "sqlite",
  enabled: true,
  storage: "disk",
  every: "15m",
  keep: { recent: "24h0m0s", daily: 14, monthly: 6 },
  volume: "data",
  path: "app.db",
  in_recipe: true,
  from_recipe: { ...all, every: false },
};

function renderDialog(backup: EffectiveBackup, opts: { days?: number; onRemove?: () => void } = {}) {
  const onSave = vi.fn();
  render(
    <BackupEditDialog
      backup={backup}
      retentionDays={opts.days ?? 0}
      targets={targets}
      open
      busy={false}
      onOpenChange={() => {}}
      onSave={onSave}
      onRemove={opts.onRemove}
    />,
  );
  return onSave;
}

describe("BackupEditDialog", () => {
  it("badges each part from template or changed, and resets one to the template", async () => {
    const user = userEvent.setup({ delay: null });
    const onSave = renderDialog(database);
    expect(screen.getAllByText("from template")).toHaveLength(2); // retention, location
    expect(screen.getByText("changed")).toBeInTheDocument(); // schedule
    expect(screen.getByLabelText("Schedule")).toHaveValue("15m");
    await user.click(screen.getByRole("button", { name: /Reset to template/ }));
    expect(screen.getByText("template's value on save")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(onSave).toHaveBeenCalledWith({ name: "database", enabled: true, storage: "disk" });
  });

  it("saves what the user changed as an override", async () => {
    const user = userEvent.setup({ delay: null });
    const onSave = renderDialog({ ...database, from_recipe: all });
    await user.selectOptions(screen.getByLabelText("Schedule"), "manual");
    const daily = screen.getByLabelText("Daily copies");
    await user.clear(daily);
    await user.type(daily, "30");
    expect(screen.getAllByText("changed")).toHaveLength(2);
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(onSave).toHaveBeenCalledWith({
      name: "database",
      enabled: true,
      storage: "disk",
      every: "manual",
      keep: { recent: "24h0m0s", daily: 30, monthly: 6 },
    });
  });

  it("moves the backup to a service with absolute paths", async () => {
    const user = userEvent.setup({ delay: null });
    const onSave = renderDialog({ ...database, from_recipe: all });
    await user.selectOptions(screen.getByLabelText("Location kind"), "service");
    expect(screen.getByLabelText("Service")).toHaveValue("web");
    const paths = screen.getByLabelText(/Database file/);
    await user.clear(paths);
    await user.type(paths, "data/app.db");
    expect(screen.getByRole("alert")).toHaveTextContent("data/app.db: use an absolute path in the container");
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
    await user.clear(paths);
    await user.type(paths, "/data/app.db");
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(onSave).toHaveBeenCalledWith({ name: "database", enabled: true, storage: "disk", service: "web", path: "/data/app.db" });
  });

  it("notes the server's ceiling and refuses a bad retention", async () => {
    const user = userEvent.setup({ delay: null });
    renderDialog(database, { days: 90 });
    expect(screen.getByText("Capped at 90 days on this server.")).toBeInTheDocument();
    const monthly = screen.getByLabelText("Monthly copies");
    await user.clear(monthly);
    await user.type(monthly, "0");
    expect(screen.queryByText("Capped at 90 days on this server.")).not.toBeInTheDocument();
    await user.type(monthly, "x");
    expect(screen.getByRole("alert")).toHaveTextContent("Use whole numbers from 0 to 3650");
  });

  it("sends the paths of a template backup only when the user changed them", async () => {
    const user = userEvent.setup({ delay: null });
    const files: EffectiveBackup = { ...database, type: "files", path: undefined, paths: ["media/"], from_recipe: all };

    // Moving it to another volume leaves the template's paths alone.
    const moved = renderDialog(files);
    await user.selectOptions(screen.getByLabelText("Volume"), "media");
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(moved).toHaveBeenCalledWith({ name: "database", enabled: true, storage: "disk", volume: "media" });
    cleanup();

    // Clearing the paths is a choice: the whole volume.
    const cleared = renderDialog(files);
    await user.clear(screen.getByLabelText(/Paths, one per line/));
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(cleared).toHaveBeenCalledWith({ name: "database", enabled: true, storage: "disk", volume: "data", paths: [] });
  });

  it("keeps a UI-added backup whole and can remove it", async () => {
    const user = userEvent.setup({ delay: null });
    const onRemove = vi.fn();
    const added: EffectiveBackup = {
      name: "uploads",
      type: "files",
      enabled: false,
      storage: "",
      every: "6h",
      keep: { recent: "24h0m0s", daily: 14, monthly: 6 },
      volume: "media",
      exclude: ["*.tmp"],
      in_recipe: false,
      from_recipe: {},
    };
    const onSave = renderDialog(added, { onRemove });
    expect(screen.queryByText("from template")).not.toBeInTheDocument();
    expect(screen.getByLabelText(/Paths, one per line/)).toHaveAttribute("placeholder", "empty: the whole volume");
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(onSave).toHaveBeenCalledWith({
      name: "uploads",
      type: "files",
      exclude: ["*.tmp"],
      every: "6h",
      keep: { recent: "24h0m0s", daily: 14, monthly: 6 },
      volume: "media",
      paths: [],
    });
    await user.click(screen.getByRole("button", { name: "Remove backup" }));
    expect(onRemove).toHaveBeenCalled();
  });

  it("shows a schedule outside the picker as it is", () => {
    renderDialog({ ...database, every: "90m" });
    expect(screen.getByLabelText("Schedule")).toHaveValue("90m");
    expect(screen.getByRole("option", { name: "Every 90m" })).toBeInTheDocument();
  });
});
