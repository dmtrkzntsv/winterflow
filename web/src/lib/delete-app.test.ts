import { describe, expect, it } from "vitest";

import { deleteOutcome } from "@/lib/delete-app";

const res = (over = {}) => ({ warnings: [] as string[], volumesDeleted: false, backupsDeleted: false, ...over });

describe("deleteOutcome", () => {
  it("names what was deleted", () => {
    expect(deleteOutcome({ withVolumes: false, withBackups: false }, res())).toEqual({ level: "success", title: "App deleted" });
    expect(deleteOutcome({ withVolumes: true, withBackups: true }, res({ volumesDeleted: true, backupsDeleted: true }))).toEqual({
      level: "success",
      title: "App, volumes and backups deleted",
    });
    expect(deleteOutcome({ withVolumes: false, withBackups: true }, res({ backupsDeleted: true })).title).toBe("App and backups deleted");
  });
  it("never claims an ignored request", () => {
    const out = deleteOutcome({ withVolumes: true, withBackups: true }, res({ volumesDeleted: true }));
    expect(out).toMatchObject({ level: "warning", title: "App deleted, but its backups were kept" });
    expect(deleteOutcome({ withVolumes: true, withBackups: true }, res()).title).toBe("App deleted, but its volumes and backups were kept");
  });
  it("shows the agent's reason when a current agent kept the backups", () => {
    const out = deleteOutcome({ withVolumes: false, withBackups: true }, res({ warnings: ["backups in disk: storage unreachable"] }));
    expect(out).toEqual({
      level: "warning",
      title: "App deleted, but its backups were kept",
      description: "backups in disk: storage unreachable",
    });
    expect(out.description).not.toContain("too old");
  });
  it("names a single extra", () => {
    expect(deleteOutcome({ withVolumes: true, withBackups: false }, res({ volumesDeleted: true })).title).toBe("App and volumes deleted");
  });
  it("passes the cleanup warnings on", () => {
    expect(deleteOutcome({ withVolumes: false, withBackups: false }, res({ warnings: ["a", "b"] }))).toEqual({
      level: "warning",
      title: "App deleted with cleanup warnings",
      description: "a\nb",
    });
  });

  it("says where the backups went when an app that had them is deleted without them", () => {
    const out = deleteOutcome({ withVolumes: false, withBackups: false }, res(), { hadBackups: true });
    expect(out).toMatchObject({ level: "success", title: "App deleted" });
    expect(out.description).toContain("Other apps' backups");
    // Nothing to say when they were deleted too, or there were none.
    expect(deleteOutcome({ withVolumes: false, withBackups: true }, res({ backupsDeleted: true }), { hadBackups: true }).description).toBeUndefined();
    expect(deleteOutcome({ withVolumes: false, withBackups: false }, res()).description).toBeUndefined();
  });
});
