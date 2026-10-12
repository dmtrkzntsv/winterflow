import { describe, expect, it } from "vitest";

import { hasRunningContainers, reloadableStaleCount, splitStatus } from "@/lib/app-status";

describe("splitStatus", () => {
  it("splits a status report into status codes and stale names", () => {
    expect(
      splitStatus([
        { app_id: "a", status_code: 1, stale_vars: ["SMTP_PASS"] },
        { app_id: "b", status_code: 5, stale_vars: [] },
        { app_id: "c", status_code: 2, stale_vars: null },
        { app_id: "d", status_code: 1 },
      ]),
    ).toEqual({ status: { a: 1, b: 5, c: 2, d: 1 }, stale: { a: ["SMTP_PASS"] } });
    expect(splitStatus(null)).toEqual({ status: {}, stale: {} });
  });
});

describe("reloadable stale apps", () => {
  it("counts only stale apps with a running container", () => {
    expect(hasRunningContainers(1)).toBe(true);
    expect(hasRunningContainers(4)).toBe(true);
    expect(hasRunningContainers(5)).toBe(false);
    expect(hasRunningContainers(0)).toBe(false);
    expect(hasRunningContainers(undefined)).toBe(false);
    expect(reloadableStaleCount({ a: ["X"], b: ["X"], c: [], d: ["X"] }, { a: 1, b: 5, c: 1 })).toBe(1);
  });
});
