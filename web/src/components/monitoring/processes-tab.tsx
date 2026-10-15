import { Fragment, useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { ArrowDown, ArrowUp, ChevronRight, Pause, Play, X } from "lucide-react";

import { DataFreshness } from "@/components/data-freshness";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
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
  groupName,
  sortProcesses,
  type AppFilter,
  type ProcessGroup,
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
  { key: "group", label: "Group", wide: true },
  { key: "threads", label: "Threads", wide: true, numeric: true },
  { key: "uptime", label: "Uptime", wide: true, numeric: true },
];
const WIDE = "hidden md:table-cell";

const GROUPS_SHOWN = 8;
const rate = (v: number | null) => (v === null ? "—" : formatRate(v));
const count = (n: number) => `${n} process${n === 1 ? "" : "es"}`;

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
  const [group, setGroup] = useState<string | null>(null);
  const [allGroups, setAllGroups] = useState(false);
  const [open, setOpen] = useState<number | null>(null);

  const appName = useMemo(() => {
    const names = new Map(apps.map((a) => [a.id, appLabel(a.name)]));
    return (id: string) => names.get(id);
  }, [apps]);
  const groups = useMemo(() => data?.groups ?? [], [data]);
  const groupLabel = useMemo(() => {
    const names = new Map(groups.map((g) => [g.id, groupName(g, appName) ?? g.id]));
    return (id: string) => names.get(id);
  }, [groups, appName]);
  const appIds = useMemo(
    () => [...new Set((data?.processes ?? []).flatMap((p) => (p.app_id ? [p.app_id] : [])))],
    [data],
  );
  // A chosen app that has left the list (stopped, gone) no longer filters:
  // the select would show nothing while the table hid every row.
  const appFilter: AppFilter = data && app.startsWith("app:") && !appIds.includes(app.slice(4)) ? "all" : app;
  // Likewise a chosen group that has left the list.
  const groupFilter = data && group !== null && !groups.some((g) => g.id === group) ? null : group;
  const rows = useMemo(
    () =>
      sortProcesses(
        filterProcesses(data?.processes ?? [], { text, app: appFilter, kernel, group: groupFilter }, appName, groupLabel),
        sort.key,
        sort.dir,
        appName,
        groupLabel,
      ),
    [data, text, appFilter, kernel, groupFilter, appName, groupLabel, sort],
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
      case "group":
        return p.group ? (
          <span className="block max-w-40 truncate text-muted-foreground">{groupLabel(p.group) ?? p.group}</span>
        ) : null;
      case "threads":
        return p.threads;
      case "uptime":
        return formatUptime(p.start_time, now);
    }
  };

  const shownGroups = allGroups ? groups : groups.slice(0, GROUPS_SHOWN);
  const groupRow = (g: ProcessGroup) => {
    const active = groupFilter === g.id;
    const name = groupName(g, appName);
    return (
      <li key={g.id}>
        <button
          type="button"
          aria-pressed={active}
          onClick={() => setGroup(active ? null : g.id)}
          className={cn(
            "grid w-full grid-cols-[minmax(0,1fr)_4.5rem_4.5rem] items-baseline gap-x-3 rounded-md px-2 py-1.5 text-left text-sm hover:bg-muted sm:grid-cols-[minmax(0,1fr)_5.5rem_5.5rem_6rem]",
            active && "bg-muted",
          )}
        >
          <span className="min-w-0 truncate">
            <span className="font-medium">{name}</span>
            {g.kind !== "container" ? <span className="ml-1.5 text-xs text-muted-foreground">{g.kind}</span> : null}
          </span>
          <span aria-label={`Read ${rate(g.read_bps)}`} className="text-right tabular-nums">{rate(g.read_bps)}</span>
          <span aria-label={`Write ${rate(g.write_bps)}`} className="text-right tabular-nums">{rate(g.write_bps)}</span>
          <span className="hidden text-right text-xs text-muted-foreground sm:block">{count(g.processes)}</span>
        </button>
      </li>
    );
  };

  return (
    <div className="space-y-4">
      {data ? (
        <Card className="gap-2 p-3.5">
          <h3 className="text-sm font-medium text-muted-foreground">Disk by app or service</h3>
          {groups.length === 0 ? (
            <p className="text-sm text-muted-foreground">Disk I/O per app or service needs cgroup v2 on this server.</p>
          ) : (
            <>
              <div
                aria-hidden="true"
                className="grid grid-cols-[minmax(0,1fr)_4.5rem_4.5rem] gap-x-3 px-2 text-xs text-muted-foreground sm:grid-cols-[minmax(0,1fr)_5.5rem_5.5rem_6rem]"
              >
                <span />
                <span className="text-right">Read/s</span>
                <span className="text-right">Write/s</span>
              </div>
              <ul aria-label="Disk by app or service">{shownGroups.map(groupRow)}</ul>
              {groups.length > GROUPS_SHOWN ? (
                <Button variant="ghost" size="sm" className="self-start" onClick={() => setAllGroups((x) => !x)}>
                  {allGroups ? "Show fewer" : `Show all (${groups.length})`}
                </Button>
              ) : null}
            </>
          )}
        </Card>
      ) : null}

      <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
        <Input
          type="search"
          aria-label="Filter processes"
          placeholder="Filter by name, command, user, PID, app or group"
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
        {groupFilter !== null ? (
          <span className="inline-flex items-center gap-1 rounded-md bg-secondary py-0.5 pl-2 pr-1 text-xs font-medium text-secondary-foreground">
            <span>Group: {groupLabel(groupFilter)}</span>
            <button
              type="button"
              aria-label="Clear group filter"
              onClick={() => setGroup(null)}
              className="rounded p-0.5 hover:bg-background/60"
            >
              <X className="size-3" />
            </button>
          </span>
        ) : null}
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
