import { describe, expect, it } from "vitest";

import {
  backupNameError,
  cappedNote,
  configOf,
  configsWith,
  durationMs,
  everyError,
  expiresNote,
  formatBytes,
  keepDays,
  keepError,
  keepLabel,
  locationError,
  locationLabel,
  needsAttention,
  pathsFrom,
  relativeAge,
  resetField,
  scheduleLabel,
  type EffectiveBackup,
} from "@/lib/backups";

const all = { every: true, keep: true, volume: true, service: true, path: true, paths: true, exclude: true };

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
const media: EffectiveBackup = {
  name: "media",
  type: "files",
  enabled: false,
  storage: "",
  every: "24h",
  keep: { recent: "24h0m0s", daily: 14, monthly: 6 },
  service: "web",
  paths: ["/app/media"],
  exclude: ["*.tmp"],
  in_recipe: true,
  from_recipe: all,
};
const audit: EffectiveBackup = {
  name: "audit",
  type: "sqlite",
  enabled: true,
  storage: "s3",
  every: "1h",
  keep: { recent: "24h0m0s", daily: 3, monthly: 0 },
  volume: "data",
  path: "audit.db",
  in_recipe: false,
  from_recipe: {},
};
const replica: EffectiveBackup = { name: "replica", type: "custom", enabled: true, storage: "s3", service: "litestream", in_recipe: true, from_recipe: all };

describe("schedules", () => {
  it("parses Go durations", () => {
    expect(durationMs("1h30m")).toBe(5_400_000);
    expect(durationMs("24h0m0s")).toBe(86_400_000);
    expect(durationMs("250ms")).toBe(250);
    expect(durationMs("")).toBeNaN();
    expect(durationMs("1 hour")).toBeNaN();
  });
  it("reads every as a label", () => {
    expect(scheduleLabel("manual")).toBe("Manual");
    expect(scheduleLabel(undefined)).toBe("Manual");
    expect(scheduleLabel("1h0m0s")).toBe("Every hour");
    expect(scheduleLabel("24h")).toBe("Every day");
    expect(scheduleLabel("90m")).toBe("Every 90m");
  });
  it("checks every like the recipe", () => {
    expect(everyError("manual")).toBe("");
    expect(everyError("5m")).toBe("");
    expect(everyError("4m")).toBe("Back up at most every 5 minutes.");
    expect(everyError("daily")).toBe("Use a duration like 15m, 2h or 24h, or Manual.");
  });
});

describe("the backups list", () => {
  it("keeps a recipe backup as its on/off, storage and overrides", () => {
    expect(configOf(database)).toEqual({ name: "database", enabled: true, storage: "disk", every: "15m" });
    expect(configOf(media)).toEqual({ name: "media" });
    expect(configOf(replica)).toEqual({ name: "replica", enabled: true, storage: "s3" });
  });
  it("keeps a UI-added backup in full", () => {
    expect(configOf(audit)).toEqual({
      name: "audit",
      type: "sqlite",
      enabled: true,
      storage: "s3",
      every: "1h",
      keep: { recent: "24h0m0s", daily: 3, monthly: 0 },
      volume: "data",
      path: "audit.db",
    });
    const custom: EffectiveBackup = { ...replica, name: "mine", in_recipe: false, from_recipe: {} };
    expect(configOf(custom)).toEqual({ name: "mine", type: "custom", enabled: true, storage: "s3", service: "litestream" });
  });
  it("sends an explicit empty paths or exclude only when it overrides the recipe", () => {
    const whole: EffectiveBackup = { ...media, service: undefined, volume: "data", paths: undefined, exclude: undefined, from_recipe: { ...all, volume: false, service: false, paths: false, exclude: false } };
    expect(configOf(whole)).toEqual({ name: "media", volume: "data", paths: [], exclude: [] });
    const recipeWhole: EffectiveBackup = { ...whole, from_recipe: all };
    expect(configOf(recipeWhole)).toEqual({ name: "media" });
    const added: EffectiveBackup = { ...whole, in_recipe: false, from_recipe: {} };
    expect(configOf(added)).toEqual({ name: "media", type: "files", every: "24h", keep: { recent: "24h0m0s", daily: 14, monthly: 6 }, volume: "data" });
  });
  it("moves a changed location as a whole", () => {
    const moved: EffectiveBackup = { ...media, service: undefined, volume: "data", paths: ["media"], from_recipe: { ...all, volume: false, service: false, paths: false } };
    expect(configOf(moved)).toEqual({ name: "media", volume: "data", paths: ["media"] });
  });
  it("replaces one entry, drops empty ones, removes and appends", () => {
    const list = [database, media, audit];
    expect(configsWith(list, "media", { name: "media", enabled: true, storage: "disk" })).toEqual([
      configOf(database),
      { name: "media", enabled: true, storage: "disk" },
      configOf(audit),
    ]);
    expect(configsWith(list, "audit", null)).toEqual([configOf(database)]);
    expect(configsWith(list, "new", { name: "new", type: "files", volume: "data" })).toEqual([
      configOf(database),
      configOf(audit),
      { name: "new", type: "files", volume: "data" },
    ]);
  });
  it("resets a field to the recipe, the location as a whole", () => {
    const c = { name: "media", enabled: true, every: "1h", service: "web", paths: ["/x"], exclude: [] };
    expect(resetField(c, "every")).toEqual({ name: "media", enabled: true, service: "web", paths: ["/x"], exclude: [] });
    expect(resetField(c, "paths")).toEqual({ name: "media", enabled: true, every: "1h", exclude: [] });
  });
});

describe("retention", () => {
  it("measures how far back it reaches", () => {
    expect(keepDays(undefined)).toBe(186);
    expect(keepDays({ recent: "720h", daily: 3, monthly: 0 })).toBe(30);
    expect(keepDays({ daily: 400, monthly: 1 })).toBe(400);
  });
  it("notes the server's ceiling only when the tiers reach past it", () => {
    expect(cappedNote(undefined, 180)).toBe("Capped at 180 days on this server.");
    expect(cappedNote({ daily: 7, monthly: 0 }, 180)).toBe("");
    expect(cappedNote(undefined, 0)).toBe("");
  });
  it("reads as a label", () => {
    expect(keepLabel({ recent: "24h0m0s", daily: 14, monthly: 6 })).toBe("24h, 14 daily, 6 monthly");
    expect(keepLabel({ recent: "36h30m0s", daily: 1, monthly: 0 })).toBe("36h30m, 1 daily, 0 monthly");
    expect(keepLabel({ recent: "45m0s" })).toBe("45m, 14 daily, 6 monthly");
    expect(keepLabel(null)).toBe("24h, 14 daily, 6 monthly");
  });
});

describe("labels", () => {
  it("reads a location", () => {
    expect(locationLabel(database)).toBe("volume data: app.db");
    expect(locationLabel(media)).toBe("service web: /app/media");
    expect(locationLabel({ type: "files", volume: "data", paths: [] })).toBe("volume data (all of it)");
    expect(locationLabel({ type: "files", volume: "data" })).toBe("volume data (all of it)");
    expect(locationLabel(replica)).toBe("service litestream");
    expect(locationLabel({ type: "sqlite", service: "web" })).toBe("service web");
  });
  it("reads ages, sizes and expiry", () => {
    const now = 1_800_000_000_000;
    const s = now / 1000;
    expect(relativeAge(s - 10, now)).toBe("just now");
    expect(relativeAge(s - 240, now)).toBe("4 min ago");
    expect(relativeAge(s - 7200, now)).toBe("2 h ago");
    expect(relativeAge(s - 86_400, now)).toBe("yesterday");
    expect(relativeAge(s - 3 * 86_400, now)).toBe("3 days ago");
    expect(formatBytes(512)).toBe("512 B");
    expect(formatBytes(1500)).toBe("1.5 kB");
    expect(formatBytes(25_000_000)).toBe("25 MB");
    expect(formatBytes(3e15)).toBe("3000 TB");
    const point = { id: "x", time: s, kind: "manual" as const, size: 1 };
    expect(expiresNote(point, now)).toBe("");
    expect(expiresNote({ ...point, expires: s + 30 * 86_400 }, now)).toBe("");
    expect(expiresNote({ ...point, expires: s + 3 * 86_400 }, now)).toBe("expires in 3 days");
    expect(expiresNote({ ...point, expires: s + 3600 }, now)).toBe("expires in 1 day");
    expect(expiresNote({ ...point, expires: s - 10 }, now)).toBe("expires today");
  });
  it("flags late and failing backups", () => {
    expect(needsAttention(undefined)).toBe(false);
    expect(needsAttention([{ name: "a", state: "ok" }, { name: "b", state: "never" }])).toBe(false);
    expect(needsAttention([{ name: "a", state: "late" }])).toBe(true);
    expect(needsAttention([{ name: "a", state: "failing" }])).toBe(true);
  });
  it("checks a new backup's name", () => {
    expect(backupNameError("audit", ["database"])).toBe("");
    expect(backupNameError("Audit", [])).toContain("lowercase");
    expect(backupNameError("database", ["database"])).toBe("The app already has a backup named database.");
  });
});

describe("the backup forms' checks", () => {
  it("splits paths by line or comma", () => {
    expect(pathsFrom(" /a \n\n/b, /c ")).toEqual(["/a", "/b", "/c"]);
    expect(pathsFrom("")).toEqual([]);
  });
  it("checks a retention", () => {
    expect(keepError("24h", "14", "6")).toBe("");
    expect(keepError("", "0", "0")).toBe("");
    expect(keepError("a day", "14", "6")).toBe("Keep every copy for a duration like 24h or 72h.");
    expect(keepError("24h", "-1", "6")).toContain("whole numbers");
    expect(keepError("24h", "14", "4000")).toContain("whole numbers");
  });
  it("checks a location", () => {
    expect(locationError("sqlite", "volume", "data", ["app.db"])).toBe("");
    expect(locationError("files", "volume", "data", [])).toBe("");
    expect(locationError("files", "volume", "", [])).toBe("Pick a volume.");
    expect(locationError("files", "service", "", [])).toBe("Pick a service.");
    expect(locationError("sqlite", "volume", "data", [])).toBe("Enter the database file's path.");
    expect(locationError("files", "service", "web", [])).toBe("Enter the paths to back up.");
    expect(locationError("files", "service", "web", ["app"])).toBe("app: use an absolute path in the container, like /app/data.");
    expect(locationError("files", "volume", "data", ["/abs"])).toBe("/abs: use a path inside the volume, like app.db or media/.");
    expect(locationError("files", "volume", "data", ["a/../b"])).toContain("inside the volume");
  });
});
