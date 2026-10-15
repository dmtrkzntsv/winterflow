import { describe, expect, it } from "vitest";

import {
  defaultDir,
  filterProcesses,
  formatUptime,
  groupName,
  sortProcesses,
  type ProcessGroup,
  type ProcessInfo,
} from "@/lib/processes";

const p = (o: Partial<ProcessInfo>): ProcessInfo => ({
  pid: 1, ppid: 0, name: "x", cmdline: "", user: "root", state: "S", threads: 1, start_time: 0,
  kernel: false, cpu_pct: null, rss: 0, mem_pct: 0, ...o,
});

const names: Record<string, string> = { "app-1": "Nextcloud" };
const appName = (id: string) => names[id];

const g = (o: Partial<ProcessGroup>): ProcessGroup => ({
  id: "g", label: "", kind: "service", processes: 1, read_bps: null, write_bps: null, ...o,
});
const GROUPS: Record<string, ProcessGroup> = {
  c1: g({ id: "c1", kind: "container", container_id: "0123456789abcdef", app_id: "app-1" }),
  s1: g({ id: "s1", label: "ssh.service" }),
};
const groupLabel = (id: string) => {
  const grp = GROUPS[id];
  return grp ? groupName(grp, appName) : undefined;
};

const LIST = [
  p({ pid: 10, name: "postgres", cmdline: "postgres: writer", user: "alice", cpu_pct: 5, rss: 300, app_id: "app-1", group: "c1" }),
  p({ pid: 2, name: "kthreadd", kernel: true }),
  p({ pid: 7, name: "sshd", cmdline: "/usr/sbin/sshd -D", cpu_pct: 20, rss: 100, group: "s1" }),
  p({ pid: 8, name: "bash", cpu_pct: 20, rss: 50 }),
];

describe("filterProcesses", () => {
  const all = { text: "", app: "all" as const, kernel: false, group: null };
  it("hides kernel threads unless asked", () => {
    expect(filterProcesses(LIST, all, appName, groupLabel).map((x) => x.pid)).toEqual([10, 7, 8]);
    expect(filterProcesses(LIST, { ...all, kernel: true }, appName, groupLabel)).toHaveLength(4);
  });
  it("matches name, command, user, pid and app name, case-insensitively", () => {
    const pids = (text: string) => filterProcesses(LIST, { ...all, text }, appName, groupLabel).map((x) => x.pid);
    expect(pids("SSHD")).toEqual([7]);
    expect(pids("writer")).toEqual([10]);
    expect(pids("alice")).toEqual([10]);
    expect(pids("8")).toEqual([8]);
    expect(pids("nextcloud")).toEqual([10]);
    expect(pids("  ")).toEqual([10, 7, 8]);
  });
  it("matches the group's name too", () => {
    const pids = (text: string) => filterProcesses(LIST, { ...all, text }, appName, groupLabel).map((x) => x.pid);
    expect(pids("ssh.service")).toEqual([7]);
    expect(pids("NEXTCLOUD")).toEqual([10]);
  });
  it("narrows to one group by id", () => {
    expect(filterProcesses(LIST, { ...all, group: "s1" }, appName, groupLabel).map((x) => x.pid)).toEqual([7]);
    expect(filterProcesses(LIST, { ...all, group: "nope" }, appName, groupLabel)).toEqual([]);
  });
  it("narrows to one app or to host processes", () => {
    expect(filterProcesses(LIST, { ...all, app: "app:app-1" }, appName, groupLabel).map((x) => x.pid)).toEqual([10]);
    expect(filterProcesses(LIST, { ...all, app: "host" }, appName, groupLabel).map((x) => x.pid)).toEqual([7, 8]);
  });
});

describe("sortProcesses", () => {
  it("sorts by a number, ties by pid, unknown values last in both directions", () => {
    expect(sortProcesses(LIST, "cpu", "desc", appName, groupLabel).map((x) => x.pid)).toEqual([7, 8, 10, 2]);
    expect(sortProcesses(LIST, "cpu", "asc", appName, groupLabel).map((x) => x.pid)).toEqual([10, 7, 8, 2]);
  });
  it("sorts text case-insensitively and apps by their name", () => {
    expect(sortProcesses(LIST, "name", "asc", appName, groupLabel).map((x) => x.name)).toEqual(["bash", "kthreadd", "postgres", "sshd"]);
    expect(sortProcesses(LIST, "app", "asc", appName, groupLabel)[0].pid).toBe(10);
  });
  it("sorts by group name, processes without a group last in both directions", () => {
    expect(sortProcesses(LIST, "group", "asc", appName, groupLabel).map((x) => x.pid)).toEqual([10, 7, 2, 8]);
    expect(sortProcesses(LIST, "group", "desc", appName, groupLabel).map((x) => x.pid)).toEqual([7, 10, 2, 8]);
  });
  it("uptime: the newest process has the least", () => {
    const l = [p({ pid: 1, start_time: 100 }), p({ pid: 2, start_time: 200 })];
    expect(sortProcesses(l, "uptime", "desc", appName, groupLabel).map((x) => x.pid)).toEqual([1, 2]);
  });
  it("does not change its input", () => {
    const copy = [...LIST];
    sortProcesses(LIST, "pid", "asc", appName, groupLabel);
    expect(LIST).toEqual(copy);
  });
});

describe("defaultDir and formatUptime", () => {
  it("numbers start descending, text ascending", () => {
    expect(defaultDir("cpu")).toBe("desc");
    expect(defaultDir("name")).toBe("asc");
    expect(defaultDir("pid")).toBe("asc");
    expect(defaultDir("group")).toBe("asc");
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

describe("groupName", () => {
  it("names a container by its app, else by its short id", () => {
    expect(groupName(GROUPS.c1, appName)).toBe("Nextcloud");
    const orphan = g({ id: "c2", kind: "container", container_id: "0123456789abcdef" });
    expect(groupName(orphan, appName)).toBe("container 0123456789ab");
  });
  it("uses the label of anything else, the id when it has none", () => {
    expect(groupName(GROUPS.s1, appName)).toBe("ssh.service");
    expect(groupName(g({ id: "/user.slice", kind: "slice" }), appName)).toBe("/user.slice");
  });
  it("is undefined without a group", () => {
    expect(groupName(undefined, appName)).toBeUndefined();
  });
});
