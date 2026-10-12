import { describe, expect, it } from "vitest";

import { isNavActive, navMain } from "@/lib/nav";

describe("navMain", () => {
  it("links the Monitoring page right after the Dashboard", () => {
    expect(navMain.map((i) => [i.title, i.url])).toEqual([
      ["Dashboard", "/"],
      ["Monitoring", "/monitoring"],
    ]);
  });
});

describe("isNavActive", () => {
  it("matches the Dashboard only on the root", () => {
    expect(isNavActive("/", "/")).toBe(true);
    expect(isNavActive("/monitoring", "/")).toBe(false);
  });

  it("matches other entries on themselves and their subpaths", () => {
    expect(isNavActive("/monitoring", "/monitoring")).toBe(true);
    expect(isNavActive("/settings/x", "/settings")).toBe(true);
    expect(isNavActive("/settingsx", "/settings")).toBe(false);
  });
});
