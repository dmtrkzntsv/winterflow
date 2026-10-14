import { describe, expect, it } from "vitest";

import {
  appAddresses,
  appsSubtitle,
  appWarning,
  containersSummary,
  serviceLabels,
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
    expect(appWarning([{ name: "a", state: "late" }, { name: "b", state: "failing" }], ["X"], 1)).toEqual({
      label: "Backup failing",
      tab: "backups",
    });
    expect(appWarning([{ name: "a", state: "late" }], undefined, 1)).toEqual({ label: "Backup late", tab: "backups" });
    expect(appWarning([{ name: "a", state: "ok" }], ["SMTP_PASS"], 1)).toEqual({ label: "Reload needed" });
  });

  it("asks for no reload of a stopped app: it reads the values on its next start", () => {
    expect(appWarning(undefined, ["SMTP_PASS"], 5)).toBeNull();
    expect(appWarning([{ name: "a", state: "late" }], ["SMTP_PASS"], 5)).toEqual({ label: "Backup late", tab: "backups" });
  });

  it("is quiet when all is well", () => {
    expect(appWarning([{ name: "a", state: "ok" }, { name: "b", state: "off" }], [], 1)).toBeNull();
    expect(appWarning(undefined, undefined, undefined)).toBeNull();
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

describe("serviceLabels", () => {
  it("drops the project prefix and the replica suffix of a single replica", () => {
    expect(serviceLabels("a1", [{ name: "wf-a1-web-1" }, { name: "wf-a1-db-1" }])).toEqual(["web", "db"]);
  });

  it("keeps the replica number of a service that has several", () => {
    expect(serviceLabels("a1", [{ name: "wf-a1-web-1" }, { name: "wf-a1-web-2" }, { name: "wf-a1-db-1" }])).toEqual([
      "web-1",
      "web-2",
      "db",
    ]);
  });

  it("leaves a name that does not carry the prefix as it is", () => {
    expect(serviceLabels("a1", [{ name: "custom_name" }])).toEqual(["custom_name"]);
  });
});

describe("containersSummary", () => {
  const c = (status_code: number) => ({ status_code });
  it("counts what runs and names what is wrong", () => {
    expect(containersSummary(1, [c(1)])).toBe("1 container running");
    expect(containersSummary(1, [c(1), c(2)])).toBe("2 containers running");
    expect(containersSummary(4, [c(1), c(3), c(4)])).toBe("1 of 3 containers running · 1 restarting · 1 with a problem");
  });

  it("says a stopped app runs nothing, and an app without a report has not reported", () => {
    expect(containersSummary(5, [])).toBe("Stopped — no containers running");
    expect(containersSummary(1, [])).toBe("No containers reported yet");
    expect(containersSummary(undefined, [])).toBe("No containers reported yet");
  });
});
