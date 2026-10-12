import { describe, expect, it } from "vitest";

import { isNavActive, navBottom, navMain, scopeNav } from "@/lib/nav";
import { serverPath } from "@/lib/server-paths";

const toServer = (sub = "") => serverPath("s1", sub);

describe("sidebar groups", () => {
  it("tops with the Dashboard alone", () => {
    expect(navMain.map((i) => [i.title, i.url])).toEqual([["Dashboard", ""]]);
  });

  it("groups Monitoring with Server Settings at the bottom", () => {
    expect(navBottom.map((i) => [i.title, i.url])).toEqual([
      ["Monitoring", "/monitoring"],
      ["Server Settings", "/settings"],
    ]);
  });

  it("scopes every entry to the server", () => {
    expect(scopeNav([...navMain, ...navBottom], toServer).map((i) => i.url)).toEqual([
      "/server/s1",
      "/server/s1/monitoring",
      "/server/s1/settings",
    ]);
  });
});

describe("isNavActive", () => {
  it("matches an exact entry (the Dashboard) only on itself", () => {
    expect(isNavActive("/server/s1", "/server/s1", true)).toBe(true);
    expect(isNavActive("/server/s1/monitoring", "/server/s1", true)).toBe(false);
  });

  it("matches other entries on themselves and their subpaths", () => {
    expect(isNavActive("/server/s1/monitoring", "/server/s1/monitoring")).toBe(true);
    expect(isNavActive("/server/s1/settings/x", "/server/s1/settings")).toBe(true);
    expect(isNavActive("/server/s1/settingsx", "/server/s1/settings")).toBe(false);
  });
});
