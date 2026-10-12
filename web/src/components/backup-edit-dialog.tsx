import { useState, type ReactNode, type RefObject } from "react";
import { CircleX, RotateCcw } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  cappedNote,
  changed,
  compactDuration,
  configOf,
  everyError,
  humanDuration,
  keepError,
  locationError,
  MANUAL,
  parseKeepRecent,
  pathsFrom,
  SCHEDULES,
  selectClass,
  type BackupConfig,
  type BackupTargets,
  type EffectiveBackup,
} from "@/lib/backups";
import { returnFocusTo } from "@/lib/focus";

// Group is a part of the form that overrides the recipe as a whole.
type Group = "schedule" | "retention" | "location";

const GROUP_FIELDS = {
  schedule: ["every"],
  retention: ["keep"],
  location: ["volume", "service", "path", "paths"],
} as const;

// BackupEditDialog edits a sqlite or files backup's schedule, retention and
// location. For a backup of the compose file each part is badged "from
// template" or "changed", and "Reset to template" drops the change (it
// shows the template's value at once and applies it when saved). A backup
// added here can be removed. The parent remounts it per opening; onSave gets
// the backup's whole new list entry; error is why the last save was refused,
// kept in view next to Save.
export function BackupEditDialog({
  backup,
  retentionDays,
  targets,
  open,
  busy,
  error = "",
  returnFocus,
  onOpenChange,
  onSave,
  onRemove,
}: {
  backup: EffectiveBackup;
  retentionDays: number;
  targets: BackupTargets | null;
  open: boolean;
  busy: boolean;
  error?: string;
  returnFocus?: RefObject<HTMLElement | null>;
  onOpenChange: (open: boolean) => void;
  onSave: (entry: BackupConfig) => void;
  onRemove?: () => void;
}) {
  const b = backup;
  const [every, setEvery] = useState(b.every ?? "1h");
  // 24h0m0s as Go prints it reads "24 hours".
  const [recent, setRecent] = useState(humanDuration(compactDuration(b.keep?.recent || "24h")));
  const [daily, setDaily] = useState(String(b.keep?.daily ?? 14));
  const [monthly, setMonthly] = useState(String(b.keep?.monthly ?? 6));
  const [where, setWhere] = useState<"volume" | "service">(b.volume ? "volume" : "service");
  const [target, setTarget] = useState(b.volume ?? b.service ?? "");
  const [pathsText, setPathsText] = useState(b.type === "sqlite" ? (b.path ?? "") : (b.paths ?? []).join("\n"));
  // The user typed the paths, or moved the backup between a volume and a
  // service (whose paths mean something else): only then are they sent for a
  // backup of the compose file. [] is a choice (the whole volume), never an echo.
  const [pathsEdited, setPathsEdited] = useState(false);
  // The paths typed for each kind of location, so switching away and back
  // finds them again.
  const [pathsByKind, setPathsByKind] = useState<Partial<Record<"volume" | "service", string>>>({});
  const [edited, setEdited] = useState<Set<Group>>(new Set());
  const [reset, setReset] = useState<Set<Group>>(new Set());

  const touch = (g: Group) => {
    setEdited((s) => new Set(s).add(g));
    setReset((s) => {
      const next = new Set(s);
      next.delete(g);
      return next;
    });
  };
  // resetGroup goes back to the compose file's value, shown at once.
  const resetGroup = (g: Group) => {
    const t = b.template;
    if (t && g === "schedule" && t.every) setEvery(t.every);
    if (t && g === "retention" && t.keep) {
      setRecent(humanDuration(compactDuration(t.keep.recent || "24h")));
      setDaily(String(t.keep.daily ?? 14));
      setMonthly(String(t.keep.monthly ?? 6));
    }
    if (t && g === "location") {
      setWhere(t.volume ? "volume" : "service");
      setTarget(t.volume ?? t.service ?? "");
      setPathsText(b.type === "sqlite" ? (t.path ?? "") : (t.paths ?? []).join("\n"));
      setPathsEdited(false);
    }
    setReset((s) => new Set(s).add(g));
    setEdited((s) => {
      const next = new Set(s);
      next.delete(g);
      return next;
    });
  };
  const isChanged = (g: Group) => !reset.has(g) && (edited.has(g) || GROUP_FIELDS[g].some((f) => changed(b, f)));

  const pathList = pathsFrom(pathsText);
  const problem =
    everyError(every) ||
    keepError(recent, daily, monthly) ||
    (reset.has("location") ? "" : locationError(b.type, where, target, pathList));
  const keep = { recent: parseKeepRecent(recent) || "24h", daily: Number(daily), monthly: Number(monthly) };
  const note = cappedNote(keep, retentionDays);

  const entry = (): BackupConfig => {
    // Fields this form doesn't edit (exclude) keep their override.
    const c = configOf(b);
    for (const g of Object.keys(GROUP_FIELDS) as Group[]) {
      const fields = GROUP_FIELDS[g];
      if (b.in_recipe && !edited.has(g)) {
        // Untouched: what is stored stays as it is, never rewritten from the
        // form; a reset drops the override, so the compose file's value applies.
        if (reset.has(g)) for (const f of fields) delete c[f];
        continue;
      }
      const [path, paths] = [c.path, c.paths];
      for (const f of fields) delete c[f];
      if (g === "schedule") c.every = every;
      if (g === "retention") c.keep = keep;
      if (g === "location") {
        c[where] = target;
        if (!b.in_recipe || pathsEdited) {
          if (b.type === "sqlite") c.path = pathList[0];
          else c.paths = pathList;
        } else {
          // A recipe backup moved without touching its paths keeps its own.
          if (path) c.path = path;
          if (paths) c.paths = paths;
        }
      }
    }
    return c;
  };

  const services = targets?.services ?? [];
  const section = (g: Group, title: string, body: ReactNode) => (
    <fieldset className="grid gap-2 rounded-md border p-3">
      <legend className="px-1 text-sm font-medium">{title}</legend>
      {b.in_recipe ? (
        <div className="flex min-h-7 flex-wrap items-center gap-2">
          {isChanged(g) ? (
            <>
              <Badge variant="secondary">changed</Badge>
              <Button type="button" variant="ghost" size="sm" className="ml-auto h-7" onClick={() => resetGroup(g)}>
                <RotateCcw className="size-3.5" /> Reset to template
              </Button>
            </>
          ) : (
            <Badge variant="outline">{reset.has(g) ? "template's value, applied on save" : "from template"}</Badge>
          )}
        </div>
      ) : null}
      {body}
    </fieldset>
  );

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        className="max-h-[calc(100dvh-2rem)] overflow-y-auto sm:max-w-lg"
        onCloseAutoFocus={returnFocusTo(returnFocus)}
      >
        <form
          className="grid gap-4"
          onSubmit={(e) => {
            e.preventDefault();
            if (!problem && !busy) onSave(entry());
          }}
        >
          <DialogHeader>
            <DialogTitle>Edit backup {b.name}</DialogTitle>
            <DialogDescription>
              {b.type === "sqlite" ? "A SQLite database file" : "Folders and files"}
              {b.in_recipe ? ", declared by the compose file." : ", added here."}
            </DialogDescription>
          </DialogHeader>
          <fieldset disabled={busy} className="grid min-w-0 gap-3">
            {section(
              "schedule",
              "Schedule",
              <select
                aria-label="Schedule"
                className={selectClass}
                value={every}
                onChange={(e) => {
                  setEvery(e.target.value);
                  touch("schedule");
                }}
              >
                {(SCHEDULES.some((s) => s.value === every) ? SCHEDULES : [...SCHEDULES, { value: every, label: `Every ${every}` }]).map(
                  (s) => (
                    <option key={s.value} value={s.value}>
                      {s.value === MANUAL ? "Manual (Back up now only)" : s.label}
                    </option>
                  ),
                )}
              </select>,
            )}
            {section(
              "retention",
              "Keep",
              <>
                <div className="grid grid-cols-3 gap-2">
                  {(
                    [
                      ["Every copy for", recent, setRecent, "keep-recent"],
                      ["Daily copies", daily, setDaily, "keep-daily"],
                      ["Monthly copies", monthly, setMonthly, "keep-monthly"],
                    ] as const
                  ).map(([label, value, set, id]) => (
                    <div key={id} className="grid gap-1">
                      <Label htmlFor={id} className="text-xs font-normal text-muted-foreground">
                        {label}
                      </Label>
                      <Input
                        id={id}
                        value={value}
                        onChange={(e) => {
                          set(e.target.value);
                          touch("retention");
                        }}
                      />
                    </div>
                  ))}
                </div>
                {note ? <p className="text-xs text-muted-foreground">{note}</p> : null}
              </>,
            )}
            {section(
              "location",
              "Location",
              <>
                <div className="grid grid-cols-[auto_minmax(0,1fr)] gap-2">
                  <select
                    aria-label="Location kind"
                    className={selectClass}
                    value={where}
                    onChange={(e) => {
                      const next = e.target.value as "volume" | "service";
                      setWhere(next);
                      // Back to the backup's own kind: its own volume or service.
                      const own = next === "volume" ? b.volume : b.service;
                      setTarget(own ?? (next === "volume" ? (targets?.volumes[0] ?? "") : (services[0]?.name ?? "")));
                      // A volume's paths are relative, a service's absolute: a
                      // path typed for the other kind means nothing here.
                      const fits = (p: string) => (next === "service" ? p.startsWith("/") : !p.startsWith("/"));
                      setPathsByKind((m) => ({ ...m, [where]: pathsText }));
                      setPathsText(pathsByKind[next] ?? (pathList.every(fits) ? pathsText : ""));
                      setPathsEdited(true);
                      touch("location");
                    }}
                  >
                    <option value="volume">Volume</option>
                    <option value="service">Service</option>
                  </select>
                  <select
                    aria-label={where === "volume" ? "Volume" : "Service"}
                    className={selectClass}
                    value={target}
                    onChange={(e) => {
                      setTarget(e.target.value);
                      touch("location");
                    }}
                  >
                    {[...new Set([target, ...(where === "volume" ? (targets?.volumes ?? []) : services.map((s) => s.name))])]
                      .filter(Boolean)
                      .map((n) => (
                        <option key={n} value={n}>
                          {n}
                        </option>
                      ))}
                  </select>
                </div>
                <div className="grid gap-1">
                  <Label htmlFor="backup-paths" className="text-xs font-normal text-muted-foreground">
                    {b.type === "sqlite" ? "Database file" : "Paths, one per line"}
                    {where === "volume" ? " (inside the volume)" : " (in the container)"}
                  </Label>
                  <Textarea
                    id="backup-paths"
                    rows={b.type === "sqlite" ? 1 : 3}
                    className="font-mono text-sm"
                    placeholder={where === "volume" ? (b.type === "sqlite" ? "app.db" : "empty: the whole volume") : "/app/data"}
                    value={pathsText}
                    onChange={(e) => {
                      setPathsText(e.target.value);
                      setPathsEdited(true);
                      touch("location");
                    }}
                  />
                </div>
                {b.type === "files" && (b.exclude ?? []).length > 0 ? (
                  <p className="text-xs text-muted-foreground [overflow-wrap:anywhere]">
                    Skipped{b.in_recipe && b.from_recipe?.exclude ? " (set by the compose file)" : ""}:{" "}
                    {(b.exclude ?? []).map((x, i) => (
                      <span key={x}>
                        {i > 0 ? ", " : ""}
                        <code className="font-mono text-foreground">{x}</code>
                      </span>
                    ))}
                  </p>
                ) : null}
              </>,
            )}
          </fieldset>
          {error ? (
            <p role="alert" className="flex items-start gap-1.5 text-sm text-destructive [overflow-wrap:anywhere]">
              <CircleX className="mt-0.5 size-4 shrink-0" /> {error}
            </p>
          ) : null}
          {problem ? (
            <p role="alert" className="flex items-start gap-1.5 text-sm text-destructive">
              <CircleX className="mt-0.5 size-4 shrink-0" /> {problem}
            </p>
          ) : null}
          <DialogFooter className="gap-2 sm:justify-between sm:space-x-0">
            {onRemove ? (
              <Button type="button" variant="ghost" className="text-destructive hover:text-destructive" disabled={busy} onClick={onRemove}>
                Remove backup
              </Button>
            ) : (
              <span />
            )}
            <div className="flex flex-col-reverse gap-2 sm:flex-row">
              <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
                Cancel
              </Button>
              <Button type="submit" disabled={!!problem || busy}>
                {busy ? "Saving…" : "Save"}
              </Button>
            </div>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
