import { describe, expect, it } from "vitest";

// The Go sources whose wording the UI matches (Vite reads them as text).
import commandSource from "../../../internal/domain/command/backup.go?raw";
import recipeSource from "../../../internal/domain/backup/recipe/effective.go?raw";

import {
  backupNameError,
  BACKUPS_CHANGED_MARKER,
  cappedNote,
  compactDuration,
  configOf,
  configsWith,
  durationMs,
  errorSummary,
  everyError,
  expiresNote,
  formatSize,
  humanDuration,
  backupNameFrom,
  isRunRefusal,
  isStaleTab,
  keepDays,
  isBackupsChanged,
  keepError,
  locationError,
  locationLabel,
  needsAttention,
  nextCopyNote,
  parseKeepRecent,
  pathsFrom,
  pointLabel,
  pointTime,
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
      keep: { recent: "24h", daily: 3, monthly: 0 },
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
    expect(configOf(added)).toEqual({ name: "media", type: "files", every: "24h", keep: { recent: "24h", daily: 14, monthly: 6 }, volume: "data" });
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
  it("reads sizes and expiry", () => {
    const now = 1_800_000_000_000;
    const s = now / 1000;
    expect(formatSize(512)).toBe("512 B");
    expect(formatSize(1500)).toBe("1.5 kB");
    expect(formatSize(25_000_000)).toBe("25 MB");
    expect(formatSize(3e15)).toBe("3000 TB");
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
  it("recognizes the agent's stale-view refusal", () => {
    expect(isBackupsChanged("the backups changed since you opened them; reload and try again")).toBe(true);
    expect(isBackupsChanged("disk full")).toBe(false);
  });
  it("splits paths by line, commas stay", () => {
    expect(pathsFrom(" /a \n\n/b, /c ")).toEqual(["/a", "/b, /c"]);
    expect(pathsFrom("")).toEqual([]);
  });
  it("checks a retention", () => {
    expect(keepError("24h", "14", "6")).toBe("");
    expect(keepError("", "0", "0")).toBe("");
    expect(keepError("a day", "14", "6")).toBe("Keep every copy for a duration like 24 hours, 7 days or 72h.");
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

describe("durations for people", () => {
  it("writes a Go duration the short way and in words", () => {
    expect(compactDuration("24h0m0s")).toBe("24h");
    expect(compactDuration("1h30m0s")).toBe("1h30m");
    expect(compactDuration("15m0s")).toBe("15m");
    expect(compactDuration("72h")).toBe("72h");
    expect(humanDuration("24h0m0s")).toBe("24 hours");
    expect(humanDuration("168h")).toBe("7 days");
    expect(humanDuration("24h")).toBe("24 hours");
    expect(humanDuration("48h")).toBe("2 days");
    expect(humanDuration("36h")).toBe("36 hours");
    expect(humanDuration("1h")).toBe("1 hour");
    expect(humanDuration("90m")).toBe("90 minutes");
    expect(humanDuration("1h30m0s")).toBe("90 minutes");
    expect(humanDuration("25h30m")).toBe("25h30m");
    expect(humanDuration("1m30s")).toBe("1m30s");
    expect(humanDuration("3h5m")).toBe("3h5m");
    expect(humanDuration("45s")).toBe("45 seconds");
    expect(humanDuration("soon")).toBe("soon");
    expect(humanDuration("0s")).toBe("0s");
  });

  it("reads back everything it writes", () => {
    for (const d of ["24h", "24h0m0s", "168h", "7d", "36h", "48h", "90m", "1h30m0s", "25h30m", "3h5m", "1m30s", "30s", "45s", "15m", "5m0s", "1h", "72h", "100m", "121m"]) {
      const words = humanDuration(d.endsWith("d") ? "168h" : d);
      const back = parseKeepRecent(words);
      expect(back, `${d} -> ${words}`).not.toBe("");
      expect(durationMs(back), `${d} -> ${words} -> ${back}`).toBe(durationMs(d.endsWith("d") ? "168h" : d));
    }
    expect(keepError("1 hour 30 minutes", "14", "6")).toBe("");
  });

  it("reads what a person types for how long to keep every copy", () => {
    expect(parseKeepRecent("24h")).toBe("24h");
    expect(parseKeepRecent("24h0m0s")).toBe("24h");
    expect(parseKeepRecent(" 24 hours ")).toBe("24h");
    expect(parseKeepRecent("1 hour")).toBe("1h");
    expect(parseKeepRecent("7 days")).toBe("168h");
    expect(parseKeepRecent("7d")).toBe("168h");
    expect(parseKeepRecent("2 weeks")).toBe("336h");
    expect(parseKeepRecent("90 minutes")).toBe("1h30m");
    expect(parseKeepRecent("1 hour 30 minutes")).toBe("1h30m");
    expect(parseKeepRecent("1 hour, 30 minutes")).toBe("1h30m");
    expect(parseKeepRecent("1 day and 2 hours")).toBe("26h");
    expect(parseKeepRecent("30 seconds")).toBe("30s");
    expect(parseKeepRecent("25h30m")).toBe("25h30m");
    expect(parseKeepRecent("1 hour 30")).toBe("");
    expect(parseKeepRecent("0 hours")).toBe("");
    expect(parseKeepRecent("1.5 days")).toBe("36h");
    expect(parseKeepRecent("1.3 hours")).toBe("1h18m");
    expect(parseKeepRecent("1.3333 hours")).toBe("");
    expect(parseKeepRecent("a day")).toBe("");
    expect(parseKeepRecent("")).toBe("");
    expect(keepError("7 days", "14", "6")).toBe("");
  });
});

describe("restore points", () => {
  it("reads the time out of a point's id and names it for people", () => {
    expect(pointTime("20261011T232354Z-pre-restore")).toBe(Date.UTC(2026, 9, 11, 23, 23, 54) / 1000);
    expect(pointTime("p3")).toBeNaN();
    const t = new Date(Date.UTC(2026, 9, 11, 23, 23, 54)).toLocaleString();
    expect(pointLabel("20261011T232354Z-pre-restore")).toBe(`pre-restore copy of ${t}`);
    expect(pointLabel("20261011T232354Z-manual")).toBe(`manual copy of ${t}`);
    expect(pointLabel("20261011T232354Z", "scheduled")).toBe(`scheduled copy of ${t}`);
    expect(pointLabel("p3", "pre-restore")).toBe("pre-restore copy");
    expect(pointLabel("whatever")).toBe("scheduled copy");
  });
});

describe("a failing backup's error", () => {
  it("is a short cause, without paths or temporary names", () => {
    expect(errorSummary("upload the copy: open /home/d/backups/disk/01a1/files/.wf-tmp-2637751049: permission denied")).toBe(
      "upload the copy: permission denied",
    );
    expect(errorSummary("database file not found: /audit.db")).toBe("database file not found");
    expect(errorSummary("disk full")).toBe("disk full");
    expect(errorSummary("first line\nsecond line")).toBe("first line");
    expect(errorSummary(`${"x".repeat(150)}`)).toHaveLength(98);
  });
});

describe("the next copy", () => {
  const at = new Date(2026, 9, 11, 12, 0, 0).getTime();
  const secs = (ms: number) => Math.floor(ms / 1000);
  const hourly = { enabled: true, type: "sqlite" as const, every: "1h" };
  it("says when the next scheduled copy is due, or that the first one is coming", () => {
    expect(nextCopyNote(hourly, "never", undefined, at)).toBe("next within 1 hour");
    const last = secs(at) - 600;
    const due = new Date(last * 1000 + 3_600_000).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" });
    expect(nextCopyNote(hourly, "ok", last, at)).toBe(`next ${due}`);
    expect(nextCopyNote(hourly, "ok", secs(at) - 7200, at)).toBe("next due now");
    const tomorrow = nextCopyNote({ ...hourly, every: "24h" }, "ok", secs(at) - 60, at);
    expect(tomorrow).toContain(new Date(at + 86_400_000 - 60_000).toLocaleDateString());
  });
  it("says nothing when nothing is scheduled or the state says more", () => {
    expect(nextCopyNote({ ...hourly, enabled: false }, "off", undefined, at)).toBe("");
    expect(nextCopyNote({ ...hourly, every: "manual" }, "never", undefined, at)).toBe("");
    expect(nextCopyNote({ ...hourly, every: undefined }, "never", undefined, at)).toBe("");
    expect(nextCopyNote({ ...hourly, every: "soon" }, "never", undefined, at)).toBe("");
    expect(nextCopyNote({ ...hourly, type: "custom" }, "ok", 1, at)).toBe("");
    expect(nextCopyNote(hourly, "failing", 1, at)).toBe("");
    expect(nextCopyNote(hourly, "running", 1, at)).toBe("");
  });
});

describe("backupNameFrom", () => {
  it("names a backup after the last segment of what was picked", () => {
    expect(backupNameFrom("media/Uploads/", [])).toBe("uploads");
    expect(backupNameFrom("/app/my_files", [])).toBe("my-files");
    expect(backupNameFrom("data", [])).toBe("data");
  });
  it("drops a database file's extension on request", () => {
    expect(backupNameFrom("db/App.sqlite3", [], true)).toBe("app");
    expect(backupNameFrom("db/App.sqlite3", [])).toBe("app-sqlite3");
    expect(backupNameFrom(".hidden", [], true)).toBe("hidden");
  });
  it("is valid, short and unique, or empty", () => {
    expect(backupNameFrom("a".repeat(60), [])).toHaveLength(28);
    expect(backupNameFrom("uploads", ["uploads", "uploads-2"])).toBe("uploads-3");
    expect(backupNameFrom("", [])).toBe("");
    expect(backupNameFrom("/", [])).toBe("");
    expect(backupNameFrom("---", [])).toBe("");
    for (const src of ["media/Uploads/", "a".repeat(60), "x.y.z"]) expect(backupNameError(backupNameFrom(src, []), [])).toBe("");
  });
});

describe("what the agent's answers are matched by", () => {
  it("pins the backups-changed marker to the Go source", () => {
    expect(commandSource).toContain(`BackupsChangedMarker = "${BACKUPS_CHANGED_MARKER}"`);
    expect(isBackupsChanged(`${BACKUPS_CHANGED_MARKER}; reload and try again`)).toBe(true);
  });
  it("pins the stale-tab refusals of recipe.Check to the Go source", () => {
    expect(recipeSource).toContain("%s is declared by the compose file; edit it instead of adding it");
    expect(recipeSource).toContain("the compose file has no backup %q: a new backup needs its type");
    expect(isStaleTab("backups[0].name: media is declared by the compose file; edit it instead of adding it")).toBe(true);
    expect(isStaleTab('backups[0]: the compose file has no backup "x": a new backup needs its type')).toBe(true);
    expect(isStaleTab("disk full")).toBe(false);
  });
  it("tells a refused run from a failed one", () => {
    expect(isRunRefusal("a backup of uploads is already queued or running")).toBe(true);
    expect(isRunRefusal("upload the copy: permission denied")).toBe(false);
  });
});
