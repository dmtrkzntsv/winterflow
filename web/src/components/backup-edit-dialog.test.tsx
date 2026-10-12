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
  template: { every: "1h", keep: { recent: "24h0m0s", daily: 14, monthly: 6 }, volume: "data", path: "app.db" },
};

function renderDialog(backup: EffectiveBackup, opts: { days?: number; error?: string; onRemove?: () => void; onChangeStorage?: () => void } = {}) {
  const onSave = vi.fn();
  render(
    <BackupEditDialog
      backup={backup}
      retentionDays={opts.days ?? 0}
      targets={targets}
      open
      busy={false}
      error={opts.error}
      onOpenChange={() => {}}
      onSave={onSave}
      onRemove={opts.onRemove}
      onChangeStorage={opts.onChangeStorage}
    />,
  );
  return onSave;
}

describe("BackupEditDialog", () => {
  it("shows the storage and offers to change it when it can", async () => {
    const user = userEvent.setup({ delay: null });
    const onChangeStorage = vi.fn();
    renderDialog(database, { onChangeStorage });
    expect(screen.getByText("disk", { selector: "span.font-mono" })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Change storage…" }));
    expect(onChangeStorage).toHaveBeenCalled();
    cleanup();
    renderDialog({ ...database, storage: "" });
    expect(screen.getByText("none")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Change storage…" })).not.toBeInTheDocument();
  });

  it("turns the backup on or off with the rest of its settings", async () => {
    const user = userEvent.setup({ delay: null });
    const onSave = renderDialog(database);
    const toggle = screen.getByRole("switch", { name: "Back up on schedule" });
    expect(toggle).toBeChecked();
    await user.click(toggle);
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(onSave.mock.calls[0][0]).toEqual({ name: "database", storage: "disk", every: "15m" });
    cleanup();
    const onSaveOn = renderDialog({ ...database, enabled: false });
    await user.click(screen.getByRole("switch", { name: "Back up on schedule" }));
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(onSaveOn.mock.calls[0][0]).toEqual({ name: "database", enabled: true, storage: "disk", every: "15m" });
  });

  it("is reduced for a custom backup: its on/off and storage, nothing else", async () => {
    const user = userEvent.setup({ delay: null });
    const replica: EffectiveBackup = { name: "replica", type: "custom", enabled: false, storage: "s3", service: "litestream", in_recipe: true, from_recipe: all };
    const onSave = renderDialog(replica, { onChangeStorage: () => {} });
    expect(screen.queryByLabelText("Schedule")).not.toBeInTheDocument();
    expect(screen.queryByLabelText(/Paths|Database file/)).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Change storage…" })).toBeInTheDocument();
    await user.click(screen.getByRole("switch", { name: "Run its backup tool" }));
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(onSave.mock.calls[0][0]).toEqual({ name: "replica", enabled: true, storage: "s3" });
  });

  it("needs a storage before the backup can be turned on", () => {
    renderDialog({ ...database, enabled: false, storage: "" }, { onChangeStorage: () => {} });
    expect(screen.getByRole("switch", { name: "Back up on schedule" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Pick a storage…" })).toBeInTheDocument();
    expect(screen.getByText(/Pick a storage to turn the backup on/)).toBeInTheDocument();
  });

  it("badges each part from template or changed, and resets one to the template", async () => {
    const user = userEvent.setup({ delay: null });
    const onSave = renderDialog(database);
    expect(screen.getAllByText("from template")).toHaveLength(2); // retention, location
    expect(screen.getByText("changed")).toBeInTheDocument(); // schedule
    expect(screen.getByLabelText("Schedule")).toHaveValue("15m");
    await user.click(screen.getByRole("button", { name: /Reset to template/ }));
    expect(screen.getByText("template's value, applied on save")).toBeInTheDocument();
    // The template's value is shown at once, not the overridden one.
    expect(screen.getByLabelText("Schedule")).toHaveValue("1h");
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(onSave).toHaveBeenCalledWith({ name: "database", enabled: true, storage: "disk" });
  });

  it("opens with a retention that is not whole hours or days, and Save enabled", () => {
    for (const recent of ["1h30m0s", "90m", "25h30m0s", "30s", "1m30s"]) {
      cleanup();
      renderDialog({ ...database, from_recipe: all, keep: { recent, daily: 14, monthly: 6 } });
      expect(screen.queryByRole("alert")).not.toBeInTheDocument();
      expect(screen.getByRole("button", { name: "Save" })).toBeEnabled();
    }
    expect(screen.getByLabelText("Every copy for")).toHaveValue("1m30s");
  });

  it("says the template's value applies on save even when the agent sent no template", async () => {
    const user = userEvent.setup({ delay: null });
    renderDialog({ ...database, template: undefined });
    await user.click(screen.getByRole("button", { name: /Reset to template/ }));
    expect(screen.getByText("template's value on save")).toBeInTheDocument();
  });

  it("shows the template's retention and location when they are reset", async () => {
    const user = userEvent.setup({ delay: null });
    const changedAll: EffectiveBackup = {
      ...database,
      from_recipe: { every: true },
      keep: { recent: "72h0m0s", daily: 3, monthly: 0 },
      volume: "media",
      path: "other.db",
    };
    const onSave = renderDialog(changedAll);
    expect(screen.getByLabelText("Every copy for")).toHaveValue("3 days");
    const resets = screen.getAllByRole("button", { name: /Reset to template/ });
    expect(resets).toHaveLength(2);
    await user.click(resets[0]);
    expect(screen.getByLabelText("Every copy for")).toHaveValue("24 hours");
    expect(screen.getByLabelText("Daily copies")).toHaveValue("14");
    expect(screen.getByLabelText("Monthly copies")).toHaveValue("6");
    await user.click(screen.getByRole("button", { name: /Reset to template/ }));
    expect(screen.getByLabelText("Volume")).toHaveValue("data");
    expect(screen.getByLabelText(/Database file/)).toHaveValue("app.db");
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(onSave).toHaveBeenCalledWith({ name: "database", enabled: true, storage: "disk" });

    // A service-addressed template with paths.
    cleanup();
    const files: EffectiveBackup = {
      ...database,
      type: "files",
      path: undefined,
      volume: "media",
      paths: ["x/"],
      from_recipe: { ...all, volume: false, paths: false },
      template: { service: "web", paths: ["/a", "/b"] },
    };
    renderDialog(files);
    await user.click(screen.getByRole("button", { name: /Reset to template/ }));
    expect(screen.getByLabelText("Service")).toHaveValue("web");
    expect(screen.getByLabelText(/Paths, one per line/)).toHaveValue("/a\n/b");
  });

  it("shows retention in words and never writes an unchanged value back", async () => {
    const user = userEvent.setup({ delay: null });
    // A stored override the user does not touch is saved as it was, in the short form.
    const stored: EffectiveBackup = { ...database, from_recipe: { ...all, keep: false }, keep: { recent: "168h0m0s", daily: 3, monthly: 0 } };
    const onSave = renderDialog(stored);
    expect(screen.getByLabelText("Every copy for")).toHaveValue("7 days");
    await user.selectOptions(screen.getByLabelText("Schedule"), "6h");
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(onSave).toHaveBeenCalledWith({
      name: "database",
      enabled: true,
      storage: "disk",
      every: "6h",
      keep: { recent: "168h", daily: 3, monthly: 0 },
    });
    cleanup();

    // Typing words is understood and saved as a Go duration.
    const typed = renderDialog({ ...database, from_recipe: all });
    const recent = screen.getByLabelText("Every copy for");
    await user.clear(recent);
    await user.type(recent, "2 days");
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(typed).toHaveBeenCalledWith({
      name: "database",
      enabled: true,
      storage: "disk",
      keep: { recent: "48h", daily: 14, monthly: 6 },
    });
    cleanup();
    renderDialog({ ...database, from_recipe: all });
    await user.type(screen.getByLabelText("Every copy for"), "x");
    expect(screen.getByRole("alert")).toHaveTextContent("a duration like 24 hours, 7 days or 72h");
  });

  it("shows why a save was refused in the dialog, and the compose file's exclusions", () => {
    renderDialog({ ...database, type: "files", exclude: ["*.tmp", "cache/"], paths: ["m/"], path: undefined, from_recipe: all }, { error: "service web: /tmp/stuff is not on a mount" });
    expect(screen.getByText("service web: /tmp/stuff is not on a mount").closest("[role=alert]")).toBeInTheDocument();
    expect(screen.getByText(/Skipped \(set by the compose file\)/)).toHaveTextContent("*.tmp, cache/");
  });

  it("clears a path that fits the other kind of location and finds it again", async () => {
    const user = userEvent.setup({ delay: null });
    renderDialog({ ...database, from_recipe: all });
    await user.selectOptions(screen.getByLabelText("Location kind"), "service");
    expect(screen.getByLabelText(/Database file/)).toHaveValue("");
    expect(screen.queryByText(/use an absolute path/)).not.toBeInTheDocument();
    await user.type(screen.getByLabelText(/Database file/), "/data/app.db");
    await user.selectOptions(screen.getByLabelText("Location kind"), "volume");
    expect(screen.getByLabelText(/Database file/)).toHaveValue("app.db");
    await user.selectOptions(screen.getByLabelText("Location kind"), "service");
    expect(screen.getByLabelText(/Database file/)).toHaveValue("/data/app.db");
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
      keep: { recent: "24h", daily: 30, monthly: 6 },
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

  it("goes back to its own volume when the location kind is switched back", async () => {
    const user = userEvent.setup({ delay: null });
    const onSave = renderDialog({ ...database, volume: "media", from_recipe: all });
    await user.selectOptions(screen.getByLabelText("Location kind"), "service");
    await user.selectOptions(screen.getByLabelText("Location kind"), "volume");
    expect(screen.getByLabelText("Volume")).toHaveValue("media");
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(onSave).toHaveBeenCalledWith({ name: "database", enabled: true, storage: "disk", volume: "media", path: "app.db" });
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
      keep: { recent: "24h", daily: 14, monthly: 6 },
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
