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
  portAddresses,
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
  it("lists the routes, never the redirects; without a covering domain the route's flag gives the scheme and no claim", () => {
    expect(appAddresses(shop)).toEqual([
      { host: "shop.example.com", url: "https://shop.example.com", tls: null },
      { host: "shop.lan", url: "http://shop.lan", tls: null },
    ]);
    expect(primaryAddress(shop)?.url).toBe("https://shop.example.com");
  });

  it("takes HTTPS from the registered domain's TLS mode and opens it by https", () => {
    const app = {
      domains: [
        { domain: "a.example.com", ssl: false, kind: "route" as const },
        { domain: "b.example.com", ssl: false, kind: "route" as const },
        { domain: "c.tunnel.example", ssl: true, kind: "route" as const },
      ],
    };
    const regs = [
      { name: "a.example.com", tls: "auto" as const },
      { name: "*.example.com", tls: "certificate" as const },
      { name: "*.tunnel.example", tls: "off" as const },
    ];
    expect(appAddresses(app, regs)).toEqual([
      { host: "a.example.com", url: "https://a.example.com", tls: "https" },
      { host: "b.example.com", url: "https://b.example.com", tls: "https" },
      { host: "c.tunnel.example", url: "https://c.tunnel.example", tls: "proxy" },
    ]);
    expect(primaryAddress(app, regs)?.tls).toBe("https");
  });

  it("opens published ports on the server's IP, leaving loopback ones out", () => {
    const ports = [
      { service: "web", host: "", port: 8090 },
      { service: "web", host: "127.0.0.1", port: 8091 },
      { service: "admin", host: "10.0.0.5", port: 9000 },
      { service: "api", host: "0.0.0.0", port: 8092 },
      { service: "api", host: "::", port: 8092 },
      { service: "", host: "::1", port: 9001 },
    ];
    expect(portAddresses(ports, "192.168.1.66")).toEqual([
      { host: "192.168.1.66:8090", url: "http://192.168.1.66:8090", tls: null, service: "web" },
      { host: "10.0.0.5:9000", url: "http://10.0.0.5:9000", tls: null, service: "admin" },
      { host: "192.168.1.66:8092", url: "http://192.168.1.66:8092", tls: null, service: "api" },
    ]);
    expect(portAddresses([{ service: "", host: "", port: 80 }], "fd00::1")).toEqual([
      { host: "[fd00::1]:80", url: "http://[fd00::1]:80", tls: null, service: undefined },
    ]);
    // Without the server's IP only an explicitly bound address is known.
    expect(portAddresses(ports, undefined).map((a) => a.host)).toEqual(["10.0.0.5:9000"]);
  });

  it("gives a port range one address and says udp, neither one a browser opens", () => {
    const ports = [
      { service: "media", host: "", port: 8000, last: 8010 },
      { service: "vpn", host: "", port: 51820, proto: "udp" },
      { service: "web", host: "", port: 8090 },
    ];
    const addresses = portAddresses(ports, "192.168.1.66");
    expect(addresses).toEqual([
      { host: "192.168.1.66:8000–8010", url: "", tls: null, service: "media" },
      { host: "192.168.1.66:51820/udp", url: "", tls: null, service: "vpn" },
      { host: "192.168.1.66:8090", url: "http://192.168.1.66:8090", tls: null, service: "web" },
    ]);
    // The one to open is the first a browser opens.
    expect(primaryAddress({ domains: [] }, [], addresses)?.host).toBe("192.168.1.66:8090");
    expect(primaryAddress({ domains: [] }, [], addresses.slice(0, 2))).toBeNull();
  });

  it("puts domains before published ports; the first is the one to open", () => {
    const port = { host: "192.168.1.66:8090", url: "http://192.168.1.66:8090", tls: null };
    expect(appAddresses(shop, [], [port]).map((a) => a.host)).toEqual(["shop.example.com", "shop.lan", "192.168.1.66:8090"]);
    expect(primaryAddress({ domains: [] }, [], [port])).toBe(port);
  });

  it("has no address without routes", () => {
    expect(primaryAddress({ domains: undefined })).toBeNull();
    expect(primaryAddress({ domains: [{ domain: "x.example.com", ssl: true, kind: "redirect" }] })).toBeNull();
  });
});

describe("counts", () => {
  it("counts the apps by what their pills read, leaving unknown ones out", () => {
    const apps = ["a", "b", "c", "d", "e", "f"].map((id) => ({ id }));
    // Only Active runs; Idle, Restarting, Problematic and Stopped each count apart; Unknown (missing) in none.
    expect(countApps(apps, { a: 1, b: 2, c: 3, d: 4, e: 5 })).toEqual({
      running: 1,
      failed: 0,
      restarting: 1,
      idle: 1,
      problem: 1,
      stopped: 1,
    });
    // A failed deploy counts as failed, whatever its code says.
    expect(countApps(apps, { a: 1, b: 2 }, { b: "boom" })).toMatchObject({ running: 1, failed: 1, idle: 0 });
  });

  it("says the server and the counts, skipping zeros except running", () => {
    const none = { running: 0, failed: 0, restarting: 0, idle: 0, problem: 0, stopped: 0 };
    expect(appsSubtitle("homelab", 4, { ...none, running: 3, stopped: 1 })).toBe("homelab · 3 running · 1 stopped");
    expect(appsSubtitle("homelab", 2, { ...none, problem: 2 })).toBe("homelab · 0 running · 2 with problems");
    expect(appsSubtitle("homelab", 5, { ...none, running: 3, failed: 1, idle: 1 })).toBe("homelab · 3 running · 1 failed · 1 idle");
    expect(appsSubtitle("homelab", 1, { ...none, restarting: 1 })).toBe("homelab · 0 running · 1 restarting");
    expect(appsSubtitle("homelab", 0, none)).toBe("homelab · no apps yet");
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
    expect(containersSummary(1, [c(1), c(2)])).toBe("1 of 2 containers running · 1 not started or paused");
    expect(containersSummary(2, [c(2)])).toBe("0 of 1 container running · 1 not started or paused");
    expect(containersSummary(4, [c(1), c(3), c(4)])).toBe("1 of 3 containers running · 1 restarting · 1 with a problem");
  });

  it("says a stopped app runs nothing, and an app without a report has not reported", () => {
    expect(containersSummary(5, [])).toBe("Stopped — no containers running");
    expect(containersSummary(1, [])).toBe("No containers reported yet");
    expect(containersSummary(undefined, [])).toBe("No containers reported yet");
  });
});
