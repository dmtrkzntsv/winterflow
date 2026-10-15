import { serviceLabels } from "@/lib/app-summary";

// ProcessInfo mirrors command.Process (process.list's result). cpu_pct is
// null when unknown (started between readings).
export type ProcessInfo = {
  pid: number;
  ppid: number;
  name: string;
  cmdline: string;
  user: string;
  state: string;
  threads: number;
  start_time: number; // unix s
  kernel: boolean;
  cpu_pct: number | null; // per core, may exceed 100
  rss: number;
  mem_pct: number;
  container_id?: string;
  app_id?: string;
  group?: string; // a ProcessGroup id
};

// ProcessGroup is a cgroup the processes run in; its disk rates are null
// when unknown (first reading, or the cgroup's io controller is off).
export type ProcessGroup = {
  id: string;
  label: string; // a container's name (empty when the agent has no status for it)
  kind: "container" | "service" | "scope" | "slice" | "other";
  container_id?: string;
  app_id?: string;
  processes: number;
  read_bps: number | null;
  write_bps: number | null;
};

export type ProcessList = {
  sampled_at: number;
  ncpu: number;
  mem_total: number;
  total: number;
  processes: ProcessInfo[];
  groups: ProcessGroup[]; // the agent sorts them: busiest writers first
};

export type SortKey = "name" | "app" | "user" | "pid" | "cpu" | "mem" | "group" | "threads" | "uptime";
export type SortDir = "asc" | "desc";
export type AppFilter = "all" | "host" | `app:${string}`;
export type ProcessFilter = { text: string; app: AppFilter; kernel: boolean; group: string | null };

type AppName = (id: string) => string | undefined;
type GroupLabel = (id: string) => string | undefined;

const TEXT_KEYS: ReadonlySet<SortKey> = new Set(["name", "app", "user", "pid", "group"]);

// groupName is what the UI calls a group: a container by its app and its
// compose service ("Nextcloud · web"; the app alone without a service), else
// by its short id; anything else by its cgroup name. groupNames works the
// services out.
export function groupName(g: ProcessGroup, appName: AppName, service = ""): string {
  if (g.kind === "container" && g.container_id) {
    const app = g.app_id ? appName(g.app_id) : undefined;
    if (!app) return `container ${g.container_id.slice(0, 12)}`;
    return service ? `${app} · ${service}` : app;
  }
  return g.label || g.id;
}

// groupNames names every group, keyed by id. An app's containers are named
// by service together, so replicas keep their number ("worker-2").
export function groupNames(groups: ProcessGroup[], appName: AppName): Map<string, string> {
  const byApp = new Map<string, ProcessGroup[]>();
  for (const g of groups) {
    if (g.kind !== "container" || !g.app_id || !g.label) continue;
    byApp.set(g.app_id, [...(byApp.get(g.app_id) ?? []), g]);
  }
  const services = new Map<string, string>();
  for (const [appId, list] of byApp) {
    const labels = serviceLabels(appId, list.map((g) => ({ name: g.label })));
    list.forEach((g, i) => services.set(g.id, labels[i]));
  }
  return new Map(groups.map((g) => [g.id, groupName(g, appName, services.get(g.id))]));
}

// defaultDir is a column's first sort direction: amounts biggest first,
// names (and pids) in order.
export function defaultDir(key: SortKey): SortDir {
  return TEXT_KEYS.has(key) ? "asc" : "desc";
}

export function filterProcesses(list: ProcessInfo[], f: ProcessFilter, appName: AppName, groupLabel: GroupLabel): ProcessInfo[] {
  const q = f.text.trim().toLowerCase();
  return list.filter((p) => {
    if (!f.kernel && p.kernel) return false;
    if (f.app === "host" && p.app_id) return false;
    if (f.app.startsWith("app:") && p.app_id !== f.app.slice(4)) return false;
    if (f.group !== null && p.group !== f.group) return false;
    if (!q) return true;
    const app = p.app_id ? (appName(p.app_id) ?? p.app_id) : "";
    const group = p.group ? (groupLabel(p.group) ?? p.group) : "";
    return [p.name, p.cmdline, p.user, String(p.pid), app, group].some((s) => s.toLowerCase().includes(q));
  });
}

function value(p: ProcessInfo, key: SortKey, appName: AppName, groupLabel: GroupLabel): number | string | null {
  switch (key) {
    case "name":
      return p.name.toLowerCase();
    case "app":
      return p.app_id ? (appName(p.app_id) ?? p.app_id).toLowerCase() : null;
    case "user":
      return p.user.toLowerCase();
    case "pid":
      return p.pid;
    case "cpu":
      return p.cpu_pct;
    case "mem":
      return p.rss;
    case "group":
      return p.group ? (groupLabel(p.group) ?? p.group).toLowerCase() : null;
    case "threads":
      return p.threads;
    case "uptime":
      // Older start = longer uptime.
      return p.start_time > 0 ? -p.start_time : null;
  }
}

// sortProcesses returns a sorted copy. Unknown values (null) go last in
// either direction; ties keep pid order so rows do not jump between polls.
export function sortProcesses(list: ProcessInfo[], key: SortKey, dir: SortDir, appName: AppName, groupLabel: GroupLabel): ProcessInfo[] {
  const sign = dir === "asc" ? 1 : -1;
  return [...list].sort((a, b) => {
    const va = value(a, key, appName, groupLabel);
    const vb = value(b, key, appName, groupLabel);
    if (va === null || vb === null) {
      if (va !== vb) return va === null ? 1 : -1;
    } else if (va !== vb) {
      return (va < vb ? -1 : 1) * sign;
    }
    return a.pid - b.pid;
  });
}

// formatUptime is how long ago a process started: 42s, 3m, 2h 5m, 3d 4h.
export function formatUptime(startUnixS: number, nowMs: number): string {
  if (!startUnixS) return "—";
  const s = Math.max(0, Math.floor(nowMs / 1000 - startUnixS));
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ${m % 60}m`;
  return `${Math.floor(h / 24)}d ${h % 24}h`;
}
