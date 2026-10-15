// ProcessInfo mirrors command.Process (process.list's result). Rates are
// null when unknown (started between readings, or /proc/<pid>/io not
// readable to the agent).
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
  read_bps: number | null;
  write_bps: number | null;
  container_id?: string;
  app_id?: string;
};

export type ProcessList = {
  sampled_at: number;
  ncpu: number;
  mem_total: number;
  total: number;
  processes: ProcessInfo[];
};

export type SortKey = "name" | "app" | "user" | "pid" | "cpu" | "mem" | "read" | "write" | "threads" | "uptime";
export type SortDir = "asc" | "desc";
export type AppFilter = "all" | "host" | `app:${string}`;
export type ProcessFilter = { text: string; app: AppFilter; kernel: boolean };

type AppName = (id: string) => string | undefined;

const TEXT_KEYS: ReadonlySet<SortKey> = new Set(["name", "app", "user", "pid"]);

// defaultDir is a column's first sort direction: amounts biggest first,
// names (and pids) in order.
export function defaultDir(key: SortKey): SortDir {
  return TEXT_KEYS.has(key) ? "asc" : "desc";
}

export function filterProcesses(list: ProcessInfo[], f: ProcessFilter, appName: AppName): ProcessInfo[] {
  const q = f.text.trim().toLowerCase();
  return list.filter((p) => {
    if (!f.kernel && p.kernel) return false;
    if (f.app === "host" && p.app_id) return false;
    if (f.app.startsWith("app:") && p.app_id !== f.app.slice(4)) return false;
    if (!q) return true;
    const app = p.app_id ? (appName(p.app_id) ?? p.app_id) : "";
    return [p.name, p.cmdline, p.user, String(p.pid), app].some((s) => s.toLowerCase().includes(q));
  });
}

function value(p: ProcessInfo, key: SortKey, appName: AppName): number | string | null {
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
    case "read":
      return p.read_bps;
    case "write":
      return p.write_bps;
    case "threads":
      return p.threads;
    case "uptime":
      // Older start = longer uptime.
      return p.start_time > 0 ? -p.start_time : null;
  }
}

// sortProcesses returns a sorted copy. Unknown values (null) go last in
// either direction; ties keep pid order so rows do not jump between polls.
export function sortProcesses(list: ProcessInfo[], key: SortKey, dir: SortDir, appName: AppName): ProcessInfo[] {
  const sign = dir === "asc" ? 1 : -1;
  return [...list].sort((a, b) => {
    const va = value(a, key, appName);
    const vb = value(b, key, appName);
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
