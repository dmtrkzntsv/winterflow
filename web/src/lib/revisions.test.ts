import { beforeEach, describe, expect, it } from "vitest";

import { forgetRevisions, keepRevisions, keptRevisions, resetRevisionsCache, revisionFlags, revisionsEpoch } from "@/lib/revisions";

const data = { current: "c", deployed: "b", revisions: [] };

describe("revisionFlags", () => {
  it("tells the live version, and a newer draft", () => {
    expect(revisionFlags(data, "b")).toEqual({ isCurrent: false, isDeployed: true, isUndeployedDraft: false });
    expect(revisionFlags(data, "c")).toEqual({ isCurrent: true, isDeployed: false, isUndeployedDraft: true });
    expect(revisionFlags({ current: "c", deployed: "c" }, "c")).toMatchObject({ isDeployed: true, isUndeployedDraft: false });
  });

  it("has no live version and no draft for an app that was never deployed", () => {
    expect(revisionFlags({ current: "c", deployed: "" }, "c")).toEqual({
      isCurrent: true,
      isDeployed: false,
      isUndeployedDraft: false,
    });
  });
});

describe("kept revisions", () => {
  beforeEach(resetRevisionsCache);

  it("keeps a read for its key, and forgets it", () => {
    keepRevisions("a1", "3", data, 0);
    expect(keptRevisions("a1", "3")).toBe(data);
    expect(keptRevisions("a1", "4")).toBeNull();
    forgetRevisions("a1");
    expect(keptRevisions("a1", "3")).toBeNull();
    expect(revisionsEpoch("a1")).toBe(1);
  });

  it("does not keep a read that began before the app was forgotten", () => {
    forgetRevisions("a1");
    keepRevisions("a1", "3", data, 0);
    expect(keptRevisions("a1", "3")).toBeNull();
  });

  it("lets a read without a key refresh what is kept, and a failed one drop it", () => {
    const newer = { current: "d", deployed: "d", revisions: [] };
    keepRevisions("a1", "3", data, 0);
    keepRevisions("a1", undefined, newer, 0);
    expect(keptRevisions("a1", "3")).toBe(newer);
    keepRevisions("a2", undefined, newer, 0);
    expect(keptRevisions("a2", "3")).toBeNull();
    keepRevisions("a1", undefined, null, 0);
    expect(keptRevisions("a1", "3")).toBeNull();
  });

  it("drops the bookkeeping of a deleted app", () => {
    forgetRevisions("a1");
    forgetRevisions("a1", { gone: true });
    expect(revisionsEpoch("a1")).toBe(0);
  });
});
