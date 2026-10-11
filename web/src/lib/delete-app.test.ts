import { describe, expect, it } from "vitest";

import { deleteOutcome } from "@/lib/delete-app";

const res = (over = {}) => ({ warnings: [] as string[], volumesDeleted: false, backupsDeleted: false, ...over });

describe("deleteOutcome", () => {
  it("names what was deleted", () => {
    expect(deleteOutcome({ withVolumes: false, withBackups: false }, res())).toEqual({ level: "success", title: "App deleted" });
    expect(deleteOutcome({ withVolumes: true, withBackups: true }, res({ volumesDeleted: true, backupsDeleted: true }))).toEqual({
      level: "success",
      title: "App and volumes and backups deleted",
    });
    expect(deleteOutcome({ withVolumes: false, withBackups: true }, res({ backupsDeleted: true })).title).toBe("App and backups deleted");
  });
  it("never claims an ignored request", () => {
    const out = deleteOutcome({ withVolumes: true, withBackups: true }, res({ volumesDeleted: true }));
    expect(out).toMatchObject({ level: "warning", title: "App deleted, but its backups were kept" });
    expect(deleteOutcome({ withVolumes: true, withBackups: true }, res()).title).toBe("App deleted, but its volumes and backups were kept");
  });
  it("passes the cleanup warnings on", () => {
    expect(deleteOutcome({ withVolumes: false, withBackups: false }, res({ warnings: ["a", "b"] }))).toEqual({
      level: "warning",
      title: "App deleted with cleanup warnings",
      description: "a\nb",
    });
  });
});
