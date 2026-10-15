import { describe, expect, it } from "vitest";

import { defaultDir, filterProcesses, formatUptime, sortProcesses, type ProcessInfo } from "@/lib/processes";

const p = (o: Partial<ProcessInfo>): ProcessInfo => ({
  pid: 1, ppid: 0, name: "x", cmdline: "", user: "root", state: "S", threads: 1, start_time: 0,
  kernel: false, cpu_pct: null, rss: 0, mem_pct: 0, read_bps: null, write_bps: null, ...o,
});

const names: Record<string, string> = { "app-1": "Nextcloud" };
const appName = (id: string) => names[id];

const LIST = [
  p({ pid: 10, name: "postgres", cmdline: "postgres: writer", user: "alice", cpu_pct: 5, rss: 300, write_bps: 100, app_id: "app-1" }),
  p({ pid: 2, name: "kthreadd", kernel: true }),
  p({ pid: 7, name: "sshd", cmdline: "/usr/sbin/sshd -D", cpu_pct: 20, rss: 100 }),
  p({ pid: 8, name: "bash", cpu_pct: 20, rss: 50 }),
];

describe("filterProcesses", () => {
  const all = { text: "", app: "all" as const, kernel: false };
  it("hides kernel threads unless asked", () => {
    expect(filterProcesses(LIST, all, appName).map((x) => x.pid)).toEqual([10, 7, 8]);
    expect(filterProcesses(LIST, { ...all, kernel: true }, appName)).toHaveLength(4);
  });
  it("matches name, command, user, pid and app name, case-insensitively", () => {
    const pids = (text: string) => filterProcesses(LIST, { ...all, text }, appName).map((x) => x.pid);
    expect(pids("SSHD")).toEqual([7]);
    expect(pids("writer")).toEqual([10]);
    expect(pids("alice")).toEqual([10]);
    expect(pids("8")).toEqual([8]);
    expect(pids("nextcloud")).toEqual([10]);
    expect(pids("  ")).toEqual([10, 7, 8]);
  });
  it("narrows to one app or to host processes", () => {
    expect(filterProcesses(LIST, { ...all, app: "app:app-1" }, appName).map((x) => x.pid)).toEqual([10]);
    expect(filterProcesses(LIST, { ...all, app: "host" }, appName).map((x) => x.pid)).toEqual([7, 8]);
  });
});

describe("sortProcesses", () => {
  it("sorts by a number, ties by pid, unknown values last in both directions", () => {
    expect(sortProcesses(LIST, "cpu", "desc", appName).map((x) => x.pid)).toEqual([7, 8, 10, 2]);
    expect(sortProcesses(LIST, "cpu", "asc", appName).map((x) => x.pid)).toEqual([10, 7, 8, 2]);
    expect(sortProcesses(LIST, "write", "desc", appName).map((x) => x.pid)).toEqual([10, 2, 7, 8]);
  });
  it("sorts text case-insensitively and apps by their name", () => {
    expect(sortProcesses(LIST, "name", "asc", appName).map((x) => x.name)).toEqual(["bash", "kthreadd", "postgres", "sshd"]);
    expect(sortProcesses(LIST, "app", "asc", appName)[0].pid).toBe(10);
  });
  it("uptime: the newest process has the least", () => {
    const l = [p({ pid: 1, start_time: 100 }), p({ pid: 2, start_time: 200 })];
    expect(sortProcesses(l, "uptime", "desc", appName).map((x) => x.pid)).toEqual([1, 2]);
  });
  it("does not change its input", () => {
    const copy = [...LIST];
    sortProcesses(LIST, "pid", "asc", appName);
    expect(LIST).toEqual(copy);
  });
});

describe("defaultDir and formatUptime", () => {
  it("numbers start descending, text ascending", () => {
    expect(defaultDir("cpu")).toBe("desc");
    expect(defaultDir("name")).toBe("asc");
    expect(defaultDir("pid")).toBe("asc");
  });
  it("formats an age", () => {
    const now = 1_700_000_000_000;
    expect(formatUptime(now / 1000 - 42, now)).toBe("42s");
    expect(formatUptime(now / 1000 - 3 * 60, now)).toBe("3m");
    expect(formatUptime(now / 1000 - (2 * 3600 + 5 * 60), now)).toBe("2h 5m");
    expect(formatUptime(now / 1000 - (3 * 86400 + 4 * 3600), now)).toBe("3d 4h");
    expect(formatUptime(0, now)).toBe("—");
  });
});
