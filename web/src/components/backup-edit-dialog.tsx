import { useState, type ReactNode } from "react";
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
  configOf,
  everyError,
  keepError,
  locationError,
  MANUAL,
  pathsFrom,
  SCHEDULES,
  selectClass,
  type BackupConfig,
  type BackupTargets,
  type EffectiveBackup,
} from "@/lib/backups";

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
// takes the template's value when saved). A backup added here can be
// removed. The parent remounts it per opening; onSave gets the backup's
// whole new list entry.
export function BackupEditDialog({
  backup,
  retentionDays,
  targets,
  open,
  busy,
  onOpenChange,
  onSave,
  onRemove,
}: {
  backup: EffectiveBackup;
  retentionDays: number;
  targets: BackupTargets | null;
  open: boolean;
  busy: boolean;
  onOpenChange: (open: boolean) => void;
  onSave: (entry: BackupConfig) => void;
  onRemove?: () => void;
}) {
  const b = backup;
  const [every, setEvery] = useState(b.every ?? "1h");
  const [recent, setRecent] = useState(b.keep?.recent ?? "24h");
  const [daily, setDaily] = useState(String(b.keep?.daily ?? 14));
  const [monthly, setMonthly] = useState(String(b.keep?.monthly ?? 6));
  const [where, setWhere] = useState<"volume" | "service">(b.volume ? "volume" : "service");
  const [target, setTarget] = useState(b.volume ?? b.service ?? "");
  const [pathsText, setPathsText] = useState(b.type === "sqlite" ? (b.path ?? "") : (b.paths ?? []).join("\n"));
  // The user typed the paths, or moved the backup between a volume and a
  // service (whose paths mean something else): only then are they sent for a
  // backup of the compose file. [] is a choice (the whole volume), never an echo.
  const [pathsEdited, setPathsEdited] = useState(false);
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
  const resetGroup = (g: Group) => {
    setReset((s) => new Set(s).add(g));
    setEdited((s) => {
      const next = new Set(s);
      next.delete(g);
      return next;
    });
  };
  const isChanged = (g: Group) => !reset.has(g) && (edited.has(g) || GROUP_FIELDS[g].some((f) => changed(b, f)));

  const paths = pathsFrom(pathsText);
  const problem =
    everyError(every) ||
    keepError(recent, daily, monthly) ||
    (reset.has("location") ? "" : locationError(b.type, where, target, paths));
  const keep = { recent: recent.trim() || "24h", daily: Number(daily), monthly: Number(monthly) };
  const note = cappedNote(keep, retentionDays);

  const entry = (): BackupConfig => {
    // Fields this form doesn't edit (exclude) keep their override.
    const c = configOf(b);
    for (const g of Object.keys(GROUP_FIELDS) as Group[]) {
      const sendPaths = g === "location" && (!b.in_recipe || pathsEdited);
      // A recipe backup keeps its own path override unless the user edited it.
      const keepPaths = g === "location" && b.in_recipe && isChanged(g) && !pathsEdited;
      for (const f of GROUP_FIELDS[g]) {
        if (!(keepPaths && (f === "path" || f === "paths"))) delete c[f];
      }
      if (b.in_recipe && !isChanged(g)) continue;
      if (g === "schedule") c.every = every;
      if (g === "retention") c.keep = keep;
      if (g === "location") {
        c[where] = target;
        if (sendPaths) {
          if (b.type === "sqlite") c.path = paths[0];
          else c.paths = paths;
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
            <Badge variant="outline">{reset.has(g) ? "template's value on save" : "from template"}</Badge>
          )}
        </div>
      ) : null}
      {body}
    </fieldset>
  );

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[calc(100dvh-2rem)] overflow-y-auto sm:max-w-lg">
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
                      setTarget(next === "volume" ? (targets?.volumes[0] ?? "") : (services[0]?.name ?? ""));
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
              </>,
            )}
          </fieldset>
          {problem ? (
            <p role="alert" className="flex items-start gap-1.5 text-sm text-destructive">
              <CircleX className="mt-0.5 size-4 shrink-0" /> {problem}
            </p>
          ) : null}
          <DialogFooter className="gap-2 sm:justify-between">
            {onRemove ? (
              <Button type="button" variant="ghost" className="text-destructive hover:text-destructive" disabled={busy} onClick={onRemove}>
                Remove backup
              </Button>
            ) : (
              <span />
            )}
            <div className="flex gap-2">
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
