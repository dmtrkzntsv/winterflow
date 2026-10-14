import { useState, type ReactNode, type RefObject } from "react";
import { RotateCcw } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { Disclosure } from "@/components/disclosure";
import { InlineError } from "@/components/inline-error";
import { NativeSelect } from "@/components/native-select";
import { PathsField, ScheduleSelect } from "@/components/backup-fields";
import { ResponsiveDialog } from "@/components/responsive-dialog";
import {
  cappedNote,
  changed,
  compactDuration,
  configOf,
  everyError,
  humanDuration,
  keepError,
  locationError,
  parseKeepRecent,
  pathsFrom,
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

// keepFields is a retention as the form's text fields; 24h0m0s as Go prints it
// reads "24 hours".
const keepFields = (k: EffectiveBackup["keep"]) => ({
  recent: humanDuration(compactDuration(k?.recent || "24h")),
  daily: String(k?.daily ?? 14),
  monthly: String(k?.monthly ?? 6),
});

// BackupEditDialog edits a sqlite or files backup's schedule and, under
// Advanced, its retention and location. For a backup of the compose file
// each part is badged "from template" or "changed", and "Reset to template"
// drops the change (it shows the template's value at once and applies it
// when saved). A backup added here can be removed. The "Back up on
// schedule" switch turns the backup on or off with the rest (it needs a
// storage). A custom backup (its own tool runs it) gets only that switch
// and the storage. The parent remounts it per opening; onSave gets the
// backup's whole new list entry; error is why the last save was refused,
// kept in view next to Save. onChangeStorage (when given) opens the storage
// picker in place of this dialog: the storage is not edited here.
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
  onChangeStorage,
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
  onChangeStorage?: () => void;
}) {
  const b = backup;
  // A custom backup runs its own tool: only its on/off and storage are here.
  const custom = b.type === "custom";
  const [enabled, setEnabled] = useState(b.enabled);
  const [every, setEvery] = useState(b.every ?? "1h");
  const [recent, setRecent] = useState(keepFields(b.keep).recent);
  const [daily, setDaily] = useState(keepFields(b.keep).daily);
  const [monthly, setMonthly] = useState(keepFields(b.keep).monthly);
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
  // A group the user changed, or reset to the template; never both.
  const [marks, setMarks] = useState<Map<Group, "edited" | "reset">>(new Map());

  // Retention and location are Advanced. A stored value the form refuses
  // opens them at once: Save is disabled until it is fixed.
  const [advancedOpen, setAdvancedOpen] = useState(
    () => !custom && !!(keepError(recent, daily, monthly) || locationError(b.type, where, target, pathsFrom(pathsText))),
  );

  const touch = (g: Group) => setMarks((m) => new Map(m).set(g, "edited"));
  // resetGroup goes back to the compose file's value, shown at once.
  const resetGroup = (g: Group) => {
    const t = b.template;
    if (t && g === "schedule" && t.every) setEvery(t.every);
    if (t && g === "retention" && t.keep) {
      const k = keepFields(t.keep);
      setRecent(k.recent);
      setDaily(k.daily);
      setMonthly(k.monthly);
    }
    if (t && g === "location") {
      setWhere(t.volume ? "volume" : "service");
      setTarget(t.volume ?? t.service ?? "");
      setPathsText(b.type === "sqlite" ? (t.path ?? "") : (t.paths ?? []).join("\n"));
      setPathsEdited(false);
    }
    setMarks((m) => new Map(m).set(g, "reset"));
  };
  const isChanged = (g: Group) => {
    const mark = marks.get(g);
    return mark !== "reset" && (mark === "edited" || GROUP_FIELDS[g].some((f) => changed(b, f)));
  };

  const pathList = pathsFrom(pathsText);
  const problem = custom
    ? ""
    : everyError(every) ||
      keepError(recent, daily, monthly) ||
      (marks.get("location") === "reset" ? "" : locationError(b.type, where, target, pathList));
  const keep = { recent: parseKeepRecent(recent) || "24h", daily: Number(daily), monthly: Number(monthly) };
  const note = cappedNote(keep, retentionDays);

  const entry = (): BackupConfig => {
    // Fields this form doesn't edit (exclude) keep their override.
    const c = configOf(b);
    if (enabled) c.enabled = true;
    else delete c.enabled;
    if (custom) return c;
    for (const g of Object.keys(GROUP_FIELDS) as Group[]) {
      const fields = GROUP_FIELDS[g];
      if (b.in_recipe && marks.get(g) !== "edited") {
        // Untouched: what is stored stays as it is, never rewritten from the
        // form; a reset drops the override, so the compose file's value applies.
        if (marks.get(g) === "reset") for (const f of fields) delete c[f];
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
            <Badge variant="outline">{marks.get(g) === "reset" ? (b.template ? "template's value, applied on save" : "template's value on save") : "from template"}</Badge>
          )}
        </div>
      ) : null}
      {body}
    </fieldset>
  );

  return (
    <ResponsiveDialog
      open={open}
      onOpenChange={onOpenChange}
      className="sm:max-w-lg"
      onCloseAutoFocus={returnFocusTo(returnFocus)}
      title={`Edit backup ${b.name}`}
      description={
        <>
          {custom ? "Runs its own backup tool" : b.type === "sqlite" ? "A SQLite database file" : "Folders and files"}
          {b.in_recipe ? ", declared by the compose file." : ", added here."}
        </>
      }
      footer={
        <div className="flex w-full flex-col-reverse gap-2 sm:flex-row sm:justify-between">
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
            <Button type="submit" form="backup-edit-form" disabled={!!problem || busy}>
              {busy ? "Saving…" : "Save"}
            </Button>
          </div>
        </div>
      }
    >
      <form
        id="backup-edit-form"
        className="grid gap-4"
        onSubmit={(e) => {
          e.preventDefault();
          if (!problem && !busy) onSave(entry());
        }}
      >
        <div className="grid gap-3 rounded-md border p-3 text-sm">
          <div className="flex items-center justify-between gap-3">
            <Label htmlFor="backup-enabled" className="font-medium">
              {custom ? "Run its backup tool" : "Back up on schedule"}
            </Label>
            <Switch id="backup-enabled" checked={enabled} disabled={busy || !b.storage} onCheckedChange={setEnabled} />
          </div>
          <div className="flex flex-wrap items-center justify-between gap-2">
            <span>
              <span className="font-medium">Storage</span>{" "}
              <span className="font-mono text-muted-foreground [overflow-wrap:anywhere]">{b.storage || "none"}</span>
            </span>
            {onChangeStorage ? (
              <Button type="button" variant="outline" size="sm" disabled={busy} onClick={onChangeStorage}>
                {b.storage ? "Change storage…" : "Pick a storage…"}
              </Button>
            ) : null}
          </div>
          {b.storage ? null : <p className="text-xs text-muted-foreground">Pick a storage to turn the backup on: its copies go there.</p>}
        </div>
        {custom ? null : (
        <fieldset disabled={busy} className="grid min-w-0 gap-3">
          {section(
            "schedule",
            "Schedule",
            <ScheduleSelect
              aria-label="Schedule"
              value={every}
              onChange={(v) => {
                setEvery(v);
                touch("schedule");
              }}
            />,
          )}
          <Disclosure label="Advanced" open={advancedOpen} onOpenChange={setAdvancedOpen}>
            {section(
              "retention",
              "Keep",
              <>
                <div className="grid grid-cols-1 gap-2 sm:grid-cols-3">
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
                  <NativeSelect
                    aria-label="Location kind"
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
                  </NativeSelect>
                  <NativeSelect
                    aria-label={where === "volume" ? "Volume" : "Service"}
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
                  </NativeSelect>
                </div>
                <PathsField
                  id="backup-paths"
                  compact
                  type={b.type}
                  where={where}
                  value={pathsText}
                  onChange={(v) => {
                    setPathsText(v);
                    setPathsEdited(true);
                    touch("location");
                  }}
                />
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
          </Disclosure>
        </fieldset>
        )}
        {error ? (
          <InlineError icon className="[overflow-wrap:anywhere]">
            {error}
          </InlineError>
        ) : null}
        {problem ? <InlineError icon>{problem}</InlineError> : null}
      </form>
    </ResponsiveDialog>
  );
}
