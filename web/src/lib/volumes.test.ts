import { describe, expect, it } from "vitest";

import type { Volume } from "@/context/use-docker";
import { groupVolumes, shortVolumeName, volumeNameError, volumeState } from "@/lib/volumes";

const HASH = "0123456789abcdef".repeat(4); // 64 hex chars

function vol(over: Partial<Volume> & { name: string }): Volume {
  return { in_use: false, orphan: false, anonymous: false, ...over };
}

describe("volumeNameError", () => {
  it("accepts docker-style names", () => {
    expect(volumeNameError("data")).toBe("");
    expect(volumeNameError("my_vol-1.2")).toBe("");
    expect(volumeNameError("9lives")).toBe("");
  });

  it("rejects a leading symbol or a forbidden character", () => {
    expect(volumeNameError("-data")).toMatch(/Start with a letter or digit/);
    expect(volumeNameError("my vol")).toMatch(/Start with a letter or digit/);
    expect(volumeNameError("a/b")).toMatch(/Start with a letter or digit/);
  });

  it("caps the length at 255", () => {
    expect(volumeNameError("a".repeat(255))).toBe("");
    expect(volumeNameError("a".repeat(256))).toBe("Use at most 255 characters.");
  });
});

describe("shortVolumeName", () => {
  it("drops the compose project prefix of an app volume", () => {
    expect(
      shortVolumeName(vol({ name: "wf-abc_data", app_id: "abc", project: "wf-abc" })),
    ).toBe("data");
  });

  it("keeps the full name when the prefix doesn't match", () => {
    expect(
      shortVolumeName(vol({ name: "other_data", app_id: "abc", project: "wf-abc" })),
    ).toBe("other_data");
  });

  it("keeps a project prefix when the volume has no app", () => {
    expect(shortVolumeName(vol({ name: "proj_data", project: "proj" }))).toBe(
      "proj_data",
    );
  });

  it("shortens an anonymous hash to docker's 12-char id", () => {
    expect(shortVolumeName(vol({ name: HASH, anonymous: true }))).toBe(
      `${HASH.slice(0, 12)}…`,
    );
  });

  it("leaves a named anonymous volume alone", () => {
    expect(shortVolumeName(vol({ name: "scratch", anonymous: true }))).toBe(
      "scratch",
    );
  });
});

describe("groupVolumes", () => {
  it("splits app, other and anonymous volumes in that order", () => {
    const a = vol({ name: "wf-1_db", app_id: "1", project: "wf-1" });
    const o = vol({ name: "plain" });
    const n = vol({ name: HASH, anonymous: true });
    const b = vol({ name: "wf-2_db", app_id: "2", project: "wf-2" });
    expect(groupVolumes([n, a, o, b])).toEqual([
      { label: "Winterflow apps", volumes: [a, b] },
      { label: "Other volumes", volumes: [o] },
      { label: "Anonymous", volumes: [n] },
    ]);
  });

  it("drops empty groups", () => {
    const o = vol({ name: "plain" });
    expect(groupVolumes([o])).toEqual([{ label: "Other volumes", volumes: [o] }]);
    expect(groupVolumes([])).toEqual([]);
  });

  it("files an anonymous volume that carries an app label under the app", () => {
    const v = vol({ name: HASH, anonymous: true, app_id: "1" });
    expect(groupVolumes([v])).toEqual([{ label: "Winterflow apps", volumes: [v] }]);
  });
});

describe("volumeState", () => {
  it("says in plain words whether a volume is used", () => {
    expect(volumeState(vol({ name: "a", in_use: true }))).toEqual({ tone: "ok", label: "In use" });
    expect(volumeState(vol({ name: "b", orphan: true, app_id: "gone" }))).toEqual({ tone: "warn", label: "Not used by any app" });
    expect(volumeState(vol({ name: "c" }))).toEqual({ tone: "idle", label: "Unused" });
  });
});
