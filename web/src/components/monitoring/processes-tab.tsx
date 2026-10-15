import { Fragment, useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { ArrowDown, ArrowUp, ChevronRight, Pause, Play } from "lucide-react";

import { DataFreshness } from "@/components/data-freshness";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useApps } from "@/context/use-apps";
import { usePageVisible } from "@/hooks/use-page-visible";
import { useProcesses } from "@/hooks/use-processes";
import { appLabel } from "@/lib/kpi";
import { formatPercent, formatRate, formatSize } from "@/lib/metrics";
import {
  defaultDir,
  filterProcesses,
  formatUptime,
  sortProcesses,
  type AppFilter,
  type ProcessInfo,
  type SortDir,
  type SortKey,
} from "@/lib/processes";
import { appPath } from "@/lib/routes";
import { cn } from "@/lib/utils";

// Column order; wide is shown from md up (on a phone the row expands).
const COLUMNS: { key: SortKey; label: string; wide?: boolean; numeric?: boolean }[] = [
  { key: "name", label: "Process" },
  { key: "app", label: "App", wide: true },
  { key: "user", label: "User", wide: true },
  { key: "pid", label: "PID", wide: true, numeric: true },
  { key: "cpu", label: "CPU", numeric: true },
  { key: "mem", label: "Memory", numeric: true },
  { key: "read", label: "Read/s", wide: true, numeric: true },
  { key: "write", label: "Write/s", numeric: true },
  { key: "threads", label: "Threads", wide: true, numeric: true },
  { key: "uptime", label: "Uptime", wide: true, numeric: true },
];
const WIDE = "hidden md:table-cell";

const rate = (v: number | null) => (v === null ? "—" : formatRate(v));

// ProcessesTab lists the server's processes, polled only while this tab is
// mounted (Monitoring mounts it only while selected), the page is visible
// and the user has not paused it.
export function ProcessesTab({ serverId }: { serverId: string }) {
  const { apps } = useApps();
  const visible = usePageVisible();
  const [paused, setPaused] = useState(false);
  const { data, error, updatedAt, refresh } = useProcesses(serverId, visible && !paused);

  const [sort, setSort] = useState<{ key: SortKey; dir: SortDir }>({ key: "cpu", dir: "desc" });
  const [text, setText] = useState("");
  const [app, setApp] = useState<AppFilter>("all");
  const [kernel, setKernel] = useState(false);
  const [open, setOpen] = useState<number | null>(null);

  const appName = useMemo(() => {
    const names = new Map(apps.map((a) => [a.id, appLabel(a.name)]));
    return (id: string) => names.get(id);
  }, [apps]);
  const appIds = useMemo(
    () => [...new Set((data?.processes ?? []).flatMap((p) => (p.app_id ? [p.app_id] : [])))],
    [data],
  );
  // A chosen app that has left the list (stopped, gone) no longer filters:
  // the select would show nothing while the table hid every row.
  const appFilter: AppFilter = data && app.startsWith("app:") && !appIds.includes(app.slice(4)) ? "all" : app;
  const rows = useMemo(
    () =>
      sortProcesses(
        filterProcesses(data?.processes ?? [], { text, app: appFilter, kernel }, appName),
        sort.key,
        sort.dir,
        appName,
      ),
    [data, text, appFilter, kernel, appName, sort],
  );
  // The agent's sample time (unix ms), so uptimes do not drift between polls.
  const now = data?.sampled_at ?? 0;

  const onSort = (key: SortKey) =>
    setSort((cur) => (cur.key === key ? { key, dir: cur.dir === "asc" ? "desc" : "asc" } : { key, dir: defaultDir(key) }));

  const cell = (p: ProcessInfo, key: SortKey) => {
    switch (key) {
      case "name":
        return (
          <div className="flex min-w-0 max-w-[45vw] items-start gap-1 md:max-w-[18rem]">
            <button
              type="button"
              aria-expanded={open === p.pid}
              aria-label={`Details for ${p.name}`}
              onClick={() => setOpen((cur) => (cur === p.pid ? null : p.pid))}
              className="-ml-1 mt-0.5 shrink-0 rounded p-0.5 text-muted-foreground hover:text-foreground md:hidden"
            >
              <ChevronRight className={cn("size-4 transition-transform", open === p.pid && "rotate-90")} />
            </button>
            <div className="min-w-0">
              <div data-testid="process-name" className="truncate font-medium">{p.name}</div>
              {p.cmdline ? (
                <div className="truncate text-xs text-muted-foreground" title={p.cmdline}>{p.cmdline}</div>
              ) : null}
            </div>
          </div>
        );
      case "app":
        return p.app_id ? (
          <Link
            to={appPath(serverId, p.app_id)}
            className="inline-flex max-w-40 truncate rounded-md bg-secondary px-2 py-0.5 text-xs font-medium text-secondary-foreground hover:underline"
          >
            {appName(p.app_id) ?? p.app_id}
          </Link>
        ) : null;
      case "user":
        return p.user;
      case "pid":
        return p.pid;
      case "cpu":
        return p.cpu_pct === null ? "—" : formatPercent(p.cpu_pct);
      case "mem":
        return (
          <>
            {formatSize(p.rss)}
            <span className="ml-1 text-xs text-muted-foreground">{formatPercent(p.mem_pct)}</span>
          </>
        );
      case "read":
        return rate(p.read_bps);
      case "write":
        return rate(p.write_bps);
      case "threads":
        return p.threads;
      case "uptime":
        return formatUptime(p.start_time, now);
    }
  };

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
        <Input
          type="search"
          aria-label="Filter processes"
          placeholder="Filter by name, command, user, PID or app"
          value={text}
          onChange={(e) => setText(e.target.value)}
          className="w-full sm:w-72"
        />
        <Select value={appFilter} onValueChange={(v) => setApp(v as AppFilter)}>
          <SelectTrigger aria-label="App" className="w-44">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">All processes</SelectItem>
            <SelectItem value="host">Host only</SelectItem>
            {appIds.map((id) => (
              <SelectItem key={id} value={`app:${id}`}>{appName(id) ?? id}</SelectItem>
            ))}
          </SelectContent>
        </Select>
        <div className="flex items-center gap-2">
          <Switch id="kernel-threads" checked={kernel} onCheckedChange={setKernel} aria-label="Show kernel threads" />
          <Label htmlFor="kernel-threads" className="text-sm font-normal">Kernel threads</Label>
        </div>
        <div className="ml-auto flex items-center gap-2 text-sm text-muted-foreground">
          {data ? (
            <span>
              {data.ncpu} cores · CPU is per core
              {data.total > data.processes.length ? ` · busiest ${data.processes.length} of ${data.total}` : ""}
            </span>
          ) : null}
          <DataFreshness
            updatedAt={updatedAt}
            onRefresh={() => {
              setPaused(false);
              refresh();
            }}
          />
          <Button variant="outline" size="sm" onClick={() => setPaused((x) => !x)} aria-label={paused ? "Resume" : "Pause"}>
            {paused ? <Play /> : <Pause />}
            {paused ? "Resume" : "Pause"}
          </Button>
        </div>
      </div>

      {error ? <p className="text-sm text-destructive">{error}</p> : null}

      {/* An error before any answer replaces the skeleton. */}
      {!data ? (
        error ? null : (
          <div data-testid="processes-loading" className="space-y-2">
            {Array.from({ length: 8 }, (_, i) => (
              <Skeleton key={i} className="h-9 w-full" />
            ))}
          </div>
        )
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              {COLUMNS.map((c) => {
                const active = sort.key === c.key;
                return (
                  <TableHead
                    key={c.key}
                    aria-sort={active ? (sort.dir === "asc" ? "ascending" : "descending") : "none"}
                    className={cn(c.wide && WIDE, c.numeric && "text-right")}
                  >
                    <button
                      type="button"
                      aria-label={`Sort by ${c.label}`}
                      onClick={() => onSort(c.key)}
                      className={cn("inline-flex items-center gap-1 hover:text-foreground", active && "text-foreground")}
                    >
                      {c.label}
                      {active ? sort.dir === "asc" ? <ArrowUp className="size-3" /> : <ArrowDown className="size-3" /> : null}
                    </button>
                  </TableHead>
                );
              })}
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.length === 0 ? (
              <TableRow>
                <TableCell colSpan={COLUMNS.length} className="py-8 text-center text-muted-foreground">
                  No processes match.
                </TableCell>
              </TableRow>
            ) : (
              rows.map((p) => (
                <Fragment key={p.pid}>
                  <TableRow data-testid="process-row">
                    {COLUMNS.map((c) => (
                      <TableCell key={c.key} className={cn(c.wide && WIDE, c.numeric && "text-right tabular-nums")}>
                        {cell(p, c.key)}
                      </TableCell>
                    ))}
                  </TableRow>
                  {open === p.pid ? (
                    <TableRow className="md:hidden">
                      <TableCell colSpan={COLUMNS.length}>
                        <dl className="grid grid-cols-2 gap-x-4 gap-y-1 text-xs">
                          {COLUMNS.filter((c) => c.wide).map((c) => (
                            <Fragment key={c.key}>
                              <dt className="text-muted-foreground">{c.label}</dt>
                              <dd className="tabular-nums">{cell(p, c.key) ?? "—"}</dd>
                            </Fragment>
                          ))}
                          {p.cmdline ? (
                            <>
                              <dt className="col-span-2 text-muted-foreground">Command</dt>
                              <dd className="col-span-2 break-all font-mono">{p.cmdline}</dd>
                            </>
                          ) : null}
                        </dl>
                      </TableCell>
                    </TableRow>
                  ) : null}
                </Fragment>
              ))
            )}
          </TableBody>
        </Table>
      )}
    </div>
  );
}
