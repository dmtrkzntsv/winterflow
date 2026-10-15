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
  label: string; // empty for containers
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

// groupName is what the UI calls a group: a container by its app (else its
// short id), anything else by its cgroup name.
export function groupName(g: ProcessGroup | undefined, appName: AppName): string | undefined {
  if (!g) return undefined;
  if (g.kind === "container" && g.container_id) {
    return (g.app_id ? appName(g.app_id) : undefined) ?? `container ${g.container_id.slice(0, 12)}`;
  }
  return g.label || g.id;
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
