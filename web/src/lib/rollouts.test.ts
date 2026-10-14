import { afterEach, describe, expect, it, vi } from "vitest";

import { createRolloutsApi, failureLine, isActive, rolloutLine, skippedLine } from "@/lib/rollouts";
import { ApiError } from "@/lib/templates";
import { fakeAPI } from "@/test/fake-api";
import { rollout } from "@/test/rollouts";

const names = (id: string) => ({ s1: "alpha", s2: "srv-2" })[id] ?? id;

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("rolloutLine", () => {
  it("counts the app in flight among the total", () => {
    expect(rolloutLine(rollout(), names)).toBe("Upgrading 3 of 12: bakomi…");
    expect(
      rolloutLine(rollout({ kind: "vars_reload", done: 1, skipped: 0, total: 5, current_app_name: "shop", current_server_id: "s2" }), names),
    ).toBe("Reloading 2 of 5: shop on srv-2…");
  });

  it("says queued, stopping, and names an app without a name by its id", () => {
    expect(rolloutLine(rollout({ state: "queued", current_app_id: "" }), names)).toBe("Upgrade to r3 queued…");
    expect(rolloutLine(rollout({ kind: "vars_reload", state: "queued", current_app_id: "" }), names)).toBe("Reload queued…");
    expect(rolloutLine(rollout({ stop_requested: true, current_app_name: "" }), names)).toBe(
      "Upgrading 3 of 12: a1b2c3d4 (stopping after this one)",
    );
  });

  it("names where a failed rollout stopped", () => {
    expect(failureLine(rollout({ state: "failed" }), names)).toBe("Upgrade to r3 stopped at bakomi");
    expect(failureLine(rollout({ kind: "vars_reload", state: "failed", current_app_name: "shop", current_server_id: "s2" }), names)).toBe(
      "Reload stopped at shop on srv-2",
    );
    // No app in flight: no dangling "at".
    expect(failureLine(rollout({ state: "failed", current_app_id: "", current_app_name: "" }), names)).toBe("Upgrade to r3 failed");
    expect(failureLine(rollout({ kind: "vars_reload", state: "failed", current_app_id: "", current_server_id: "" }), names)).toBe(
      "Reload failed",
    );
    expect(isActive(rollout())).toBe(true);
    expect(isActive(rollout({ state: "failed" }))).toBe(false);
  });
});

describe("skippedLine", () => {
  it("says what an ended Upgrade all skipped, pluralized", () => {
    expect(skippedLine(rollout({ state: "done", done: 2, skipped: 1, revision: 5 }))).toBe(
      "Upgraded 2 apps to r5 · 1 skipped: it needs a new value — upgrade it on its own.",
    );
    expect(skippedLine(rollout({ state: "done", done: 1, skipped: 3, revision: 5 }))).toBe(
      "Upgraded 1 app to r5 · 3 skipped: they need a new value — upgrade them on their own.",
    );
    expect(skippedLine(rollout({ state: "done", done: 0, skipped: 1, revision: 5 }))).toBe(
      "No app upgraded to r5 · 1 skipped: it needs a new value — upgrade it on its own.",
    );
    expect(skippedLine(rollout({ state: "stopped", done: 2, skipped: 1, revision: 5 }))).toBe(
      "Stopped after upgrading 2 apps to r5 · 1 skipped: it needs a new value — upgrade it on its own.",
    );
    expect(skippedLine(rollout({ state: "stopped", done: 0, skipped: 2, revision: 5 }))).toBe(
      "Stopped before upgrading any app to r5 · 2 skipped: they need a new value — upgrade them on their own.",
    );
  });

  it("is null when nothing was skipped, the rollout is not an upgrade, or it has not ended well", () => {
    expect(skippedLine(rollout({ state: "done", done: 2, skipped: 0 }))).toBeNull();
    expect(skippedLine(rollout({ state: "done", kind: "vars_reload", skipped: 2 }))).toBeNull();
    expect(skippedLine(rollout({ state: "failed", skipped: 2 }))).toBeNull();
    expect(skippedLine(rollout({ state: "running", skipped: 2 }))).toBeNull();
  });
});

describe("createRolloutsApi", () => {
  it("lists, stops and queues Upgrade all", async () => {
    const api = fakeAPI({
      "/api/v1/rollout/get-rollouts": { rollouts: null },
      "/api/v1/rollout/stop-rollout": rollout({ stop_requested: true }),
      "/api/v1/template/upgrade-instances": rollout({ state: "queued" }),
    });
    const r = createRolloutsApi();
    expect(await r.list("template_upgrade", "t1")).toEqual([]);
    await r.list("vars_reload");
    expect((await r.stop("r1")).stop_requested).toBe(true);
    expect((await r.upgradeAll("t1", 3)).state).toBe("queued");
    expect(api.calls.map((c) => c.query)).toEqual(["?kind=template_upgrade&scope_key=t1", "?kind=vars_reload", "", ""]);
    expect(api.posted("/api/v1/rollout/stop-rollout")).toEqual([{ id: "r1" }]);
    expect(api.posted("/api/v1/template/upgrade-instances")).toEqual([{ id: "t1", revision: 3 }]);
  });

  it("refuses with the API's status", async () => {
    fakeAPI({ "/api/v1/template/upgrade-instances": Object.assign(new Error("an upgrade is already running"), { status: 409 }) });
    const err = await createRolloutsApi().upgradeAll("t1", 3).catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect((err as ApiError).status).toBe(409);
    expect((err as ApiError).message).toBe("an upgrade is already running");
  });
});
