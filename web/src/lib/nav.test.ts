import { describe, expect, it } from "vitest";

import { isNavActive, navBottom, navMain } from "@/lib/nav";

describe("sidebar groups", () => {
  it("tops with the Dashboard alone", () => {
    expect(navMain.map((i) => [i.title, i.url])).toEqual([["Dashboard", "/"]]);
  });

  it("groups Monitoring with Server Settings at the bottom", () => {
    expect(navBottom.map((i) => [i.title, i.url])).toEqual([
      ["Monitoring", "/monitoring"],
      ["Server Settings", "/settings"],
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
