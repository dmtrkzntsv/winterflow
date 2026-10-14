import { describe, expect, it } from "vitest";

import { isNavActive, sidebarNav } from "@/lib/nav";

const urls = (n: ReturnType<typeof sidebarNav>) => [n.apps, ...n.pages, n.settings].map((i) => [i.title, i.url]);

describe("sidebarNav", () => {
  it("points Apps and Monitoring into the server and the rest at the org", () => {
    expect(urls(sidebarNav("s1"))).toEqual([
      ["Apps", "/server/s1"],
      ["Monitoring", "/server/s1/monitoring"],
      ["Inbox", "/inbox"],
      ["Templates", "/templates"],
      ["Settings", "/settings"],
    ]);
    expect(sidebarNav("s1").pages.find((p) => p.title === "Inbox")?.badge).toBe("inbox");
  });

  it("has no Monitoring before there is a server", () => {
    expect(urls(sidebarNav(null))).toEqual([
      ["Apps", "/"],
      ["Inbox", "/inbox"],
      ["Templates", "/templates"],
      ["Settings", "/settings"],
    ]);
  });

  it("lights Apps on the apps page only, and Settings on both settings areas", () => {
    const nav = sidebarNav("s1");
    expect(nav.apps.isActive("/server/s1")).toBe(true);
    expect(nav.apps.isActive("/server/s1/app/a1")).toBe(false);
    expect(nav.settings.isActive("/settings/members")).toBe(true);
    expect(nav.settings.isActive("/server/s1/settings/docker")).toBe(true);
    expect(nav.settings.isActive("/server/s1/monitoring")).toBe(false);
    expect(nav.pages[0].isActive("/server/s1/monitoring")).toBe(true);
  });
});

describe("isNavActive", () => {
  it("matches an exact entry only on itself", () => {
    expect(isNavActive("/server/s1", "/server/s1", true)).toBe(true);
    expect(isNavActive("/server/s1/monitoring", "/server/s1", true)).toBe(false);
  });

  it("matches other entries on themselves and their subpaths", () => {
    expect(isNavActive("/server/s1/monitoring", "/server/s1/monitoring")).toBe(true);
    expect(isNavActive("/server/s1/settings/x", "/server/s1/settings")).toBe(true);
    expect(isNavActive("/server/s1/settingsx", "/server/s1/settings")).toBe(false);
  });
});
