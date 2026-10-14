import { describe, expect, it } from "vitest";

import {
  appAddresses,
  appsSubtitle,
  appWarning,
  containerHint,
  countApps,
  filterApps,
  primaryAddress,
} from "@/lib/app-summary";

const shop = {
  name: "Shop",
  domains: [
    { domain: "old.example.com", ssl: true, kind: "redirect" as const },
    { domain: "shop.example.com", ssl: true, kind: "route" as const },
    { domain: "shop.lan", ssl: false, kind: "route" as const },
  ],
};

describe("addresses", () => {
  it("lists the routes, never the redirects, with the scheme TLS gives", () => {
    expect(appAddresses(shop)).toEqual([
      { host: "shop.example.com", url: "https://shop.example.com", https: true },
      { host: "shop.lan", url: "http://shop.lan", https: false },
    ]);
    expect(primaryAddress(shop)?.url).toBe("https://shop.example.com");
  });

  it("has no address without routes", () => {
    expect(primaryAddress({ domains: undefined })).toBeNull();
    expect(primaryAddress({ domains: [{ domain: "x.example.com", ssl: true, kind: "redirect" }] })).toBeNull();
  });
});

describe("counts", () => {
  it("counts running, problem and stopped apps, leaving unknown ones out", () => {
    const apps = ["a", "b", "c", "d", "e", "f"].map((id) => ({ id }));
    // Active, Idle, Restarting run; Problematic is a problem; Stopped; Unknown (missing).
    expect(countApps(apps, { a: 1, b: 2, c: 3, d: 4, e: 5 })).toEqual({ running: 3, problem: 1, stopped: 1 });
  });

  it("says the server and the counts, skipping zeros except running", () => {
    expect(appsSubtitle("homelab", 4, { running: 3, problem: 0, stopped: 1 })).toBe("homelab · 3 running · 1 stopped");
    expect(appsSubtitle("homelab", 2, { running: 0, problem: 2, stopped: 0 })).toBe("homelab · 0 running · 2 with problems");
    expect(appsSubtitle("homelab", 0, { running: 0, problem: 0, stopped: 0 })).toBe("homelab · no apps yet");
  });
});

describe("appWarning", () => {
  it("names the worst backup first, then shared variables to reload", () => {
    expect(appWarning([{ name: "a", state: "late" }, { name: "b", state: "failing" }], ["X"])).toEqual({
      label: "Backup failing",
      tab: "backups",
    });
    expect(appWarning([{ name: "a", state: "late" }], undefined)).toEqual({ label: "Backup late", tab: "backups" });
    expect(appWarning([{ name: "a", state: "ok" }], ["SMTP_PASS"])).toEqual({ label: "Reload needed" });
  });

  it("is quiet when all is well", () => {
    expect(appWarning([{ name: "a", state: "ok" }, { name: "b", state: "off" }], [])).toBeNull();
    expect(appWarning(undefined, undefined)).toBeNull();
  });
});

describe("filterApps", () => {
  const apps = [shop, { name: "Blog", domains: [] }, { name: "Photos", domains: undefined }];

  it("matches names and addresses, ignoring case and spaces", () => {
    expect(filterApps(apps, "  BLO ").map((a) => a.name)).toEqual(["Blog"]);
    expect(filterApps(apps, "lan").map((a) => a.name)).toEqual(["Shop"]);
  });

  it("keeps everything for an empty query", () => {
    expect(filterApps(apps, "")).toBe(apps);
  });
});

describe("containerHint", () => {
  it("says what is wrong in a few words", () => {
    const base = { container_id: "c", name: "web", exit_code: 0 };
    expect(containerHint({ ...base, status_code: 1 })).toBe("");
    expect(containerHint({ ...base, status_code: 3 })).toBe("Keeps restarting");
    expect(containerHint({ ...base, status_code: 4, exit_code: 137 })).toBe("Stopped with exit code 137");
    expect(containerHint({ ...base, status_code: 4, error: "OOMKilled\nmore detail" })).toBe("OOMKilled");
    expect(containerHint({ ...base, status_code: 4 })).toBe("Has a problem");
  });
});
