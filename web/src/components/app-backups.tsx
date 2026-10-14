import { useCallback, useEffect, useRef, useState } from "react";
import { MoreHorizontal, Plus, RefreshCw } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { Spinner } from "@/components/ui/spinner";
import { AddBackupDialog } from "@/components/add-backup-dialog";
import { BackupEditDialog } from "@/components/backup-edit-dialog";
import { BackupStorageDialog } from "@/components/backup-storage-dialog";
import { InlineError } from "@/components/inline-error";
import { RestorePointsDialog } from "@/components/restore-points-dialog";
import { RowList } from "@/components/section";
import { StatusPill } from "@/components/status";
import { useApps } from "@/context/use-apps";
import {
  backupRowState,
  holdHealth,
  releaseHealth,
  restoringBackup,
  shownHealth,
  useActivity,
  useOverlays,
  type Activity,
  type Outcome,
} from "@/lib/backup-activity";
import { useBackups } from "@/context/use-backups";
import { useRunBackup } from "@/hooks/use-run-backup";
import {
  configOf,
  configsWith,
  errorSummary,
  isBackupsChanged,
  isStaleTab,
  locationLabel,
  scheduleLabel,
  type BackupConfig,
  type BackupHealth,
  type BackupTargets,
  type EffectiveBackup,
} from "@/lib/backups";
import { backupStatus, RESTORING } from "@/lib/status";
import { cn, relativeTime } from "@/lib/utils";

// base identifies the stored list this view was loaded with (see save).
type Detail = { list: EffectiveBackup[]; targets: BackupTargets | null; days: number; error: string; base: string };

// Open is which dialog shows, for which backup.
type Open =
  | { kind: "enable"; name: string } // Set up: pick the storage and turn the backup on
  | { kind: "storage"; name: string } // change the storage of a backup that has one
  | { kind: "edit"; name: string }
  | { kind: "points"; name: string }
  | { kind: "add" }
  | null;

const sameOpen = (a: Open, b: Open) => JSON.stringify(a) === JSON.stringify(b);

// AppBackups is the app page's Backups tab: each backup (declared by the
// compose file, or added here) with its storage, schedule, last copy and
// health; Set up for one that has no storage yet (it picks the storage and
// turns the backup on); its actions in a ⋯ menu (Back up now, Restore, Edit:
// on/off, schedule, retention, location and storage); and Add backup (which
// picks the storage and starts the backup on).
export function AppBackups({ appId }: { appId: string }) {
  const { getApp, backupsByApp } = useApps();
  const backups = useBackups();
  const runNow = useRunBackup(appId);
  const [detail, setDetail] = useState<Detail | null>(null);
  const [loadError, setLoadError] = useState("");
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState<string | null>(null); // the backup being saved ("" = the list)
  // What runs (Back up now, Restore) outlives this tab: see backup-activity.
  const activity = useActivity(appId);
  const restoring = restoringBackup(activity);
  const [open, setOpen] = useState<Open>(null);
  const [openKey, setOpenKey] = useState(0);
  // Why the open dialog's last save was refused: it stays in the dialog.
  const [saveError, setSaveError] = useState("");
  // The control that opened the dialog: focus goes back to it on close.
  const opener = useRef<HTMLElement | null>(null);
  // What the user was just told outranks a health snapshot older than it.
  const overlays = useOverlays(appId);
  const [, expire] = useState(0);
  const reported = backupsByApp?.[appId];
  // A report that shows an operation's effect (or time running out) ends the
  // hold for good: later reports are followed as they are.
  useEffect(() => {
    const at = Date.now();
    for (const [name, o] of Object.entries(overlays)) {
      if (at >= o.until || o.shows((reported ?? []).find((h) => h.name === name))) releaseHealth(appId, name);
    }
  }, [appId, overlays, reported]);
  const nextExpiry = Math.min(Infinity, ...Object.values(overlays).map((o) => o.until));
  useEffect(() => {
    if (!Number.isFinite(nextExpiry)) return;
    const t = setTimeout(() => expire((n) => n + 1), Math.max(0, nextExpiry - Date.now()) + 50);
    return () => clearTimeout(t);
  }, [nextExpiry]);

  const load = useCallback(async () => {
    setLoading(true);
    setLoadError("");
    try {
      const d = await getApp(appId);
      setDetail({ list: d.backups ?? [], targets: d.backup_targets ?? null, days: d.retention_days ?? 0, error: d.backups_error ?? "", base: d.backups_base ?? "" });
    } catch (e) {
      setLoadError(e instanceof Error ? e.message : "Failed to load the backups");
    } finally {
      setLoading(false);
    }
  }, [appId, getApp]);

  useEffect(() => {
    void load();
  }, [load]);

  const show = (next: Open, trigger?: HTMLElement | null) => {
    opener.current = trigger ?? null;
    setSaveError("");
    setOpenKey((k) => k + 1);
    setOpen(next);
  };

  const list = detail?.list ?? [];
  // A list that can't be read comes back empty: building an update from it
  // would replace every stored setting with the one change.
  const locked = !!detail?.error;
  const snapshot = new Map((backupsByApp?.[appId] ?? []).map((h) => [h.name, h]));
  const now = Date.now();
  const healthOf = (name: string) => shownHealth(overlays[name], snapshot.get(name), now);

  // save replaces the app's whole list, with name's entry replaced by entry
  // (null: removed; a new name: added). The agent refuses it when the stored
  // list is no longer the one this view loaded (someone saved meanwhile):
  // the tab then reloads instead of undoing their change.
  const save = async (name: string, entry: BackupConfig | null, done: string, outcome?: Outcome) => {
    if (locked) return;
    const started = open; // the dialog this save belongs to
    setBusy(name);
    setSaveError("");
    try {
      const prior = healthOf(name);
      const res = await backups.updateBackups(appId, configsWith(list, name, entry), detail?.base);
      if (outcome) holdHealth(appId, name, outcome, prior);
      setDetail((d) => (d ? { ...d, list: res.backups, base: res.base || d.base } : d));
      setOpen((cur) => (sameOpen(cur, started) ? null : cur));
      const description = [res.redeployed ? "The app was redeployed." : "", ...res.warnings].filter(Boolean).join("\n");
      if (res.warnings.length > 0) toast.warning(done, { description });
      else toast.success(done, { description: description || undefined });
    } catch (e) {
      const message = e instanceof Error ? e.message : "";
      if (isBackupsChanged(message) || isStaleTab(message)) {
        // Someone saved meanwhile, or the compose file changed under this
        // view: nothing was saved, and the view is stale.
        setOpen(null);
        toast.error("The backups were changed elsewhere", {
          description: isStaleTab(message) ? `${message}. This page was out of date; it was reloaded, so try again.` : message,
        });
        void load();
      } else {
        // The toast fades; the dialog keeps the reason next to Save.
        setSaveError(message || "The backups couldn't be saved.");
        toast.error("Couldn't save the backups", { description: message || undefined });
      }
    } finally {
      setBusy(null);
    }
  };

  if (loading && !detail) {
    return (
      <div className="flex h-40 items-center justify-center">
        <Spinner />
      </div>
    );
  }
  if (loadError && !detail) {
    return (
      <div className="flex h-40 flex-col items-center justify-center gap-3">
        <p className="text-sm text-destructive">{loadError}</p>
        <Button size="sm" variant="outline" onClick={() => void load()}>
          Try again
        </Button>
      </div>
    );
  }

  const current = open && open.kind !== "add" ? list.find((b) => b.name === open.name) : undefined;
  return (
    <div className="max-w-5xl space-y-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        {list.length > 0 ? (
          <p className="max-w-2xl text-sm text-muted-foreground">
            Backups copy this app's data to one of the server's storages on a schedule. Backups the compose file declares are
            listed ready to set up.
          </p>
        ) : (
          <span />
        )}
        <div className="flex items-center gap-2">
          <Button
            size="icon"
            variant="ghost"
            className="size-8 text-muted-foreground"
            aria-label="Refresh backups"
            title="Refresh backups"
            onClick={() => void load()}
            disabled={loading}
          >
            <RefreshCw className={cn("size-4", loading && "animate-spin")} />
          </Button>
          <Button size="sm" onClick={(e) => show({ kind: "add" }, e.currentTarget)} disabled={busy !== null || !!restoring || locked}>
            <Plus className="size-4" /> Add backup
          </Button>
        </div>
      </div>
      {restoring ? (
        <p role="status" className="flex items-start gap-2 rounded-md bg-sky-500/10 px-3 py-2 text-sm text-sky-800 dark:text-sky-300">
          <Spinner aria-hidden className="mt-0.5 size-4 shrink-0" />
          <span>
            Restoring <strong className="font-mono font-medium">{restoring}</strong>: the app is stopped until it is done, which can take a while for
            a large backup. You can leave this page; the result shows up here when it ends.
          </span>
        </p>
      ) : null}
      {detail?.error ? (
        <InlineError className="rounded-md bg-destructive/10 px-3 py-2 [overflow-wrap:anywhere]">
          The backups can't be read: {detail.error}. Fix the compose file, or roll back in History (Configuration → History), before changing the backups.
        </InlineError>
      ) : null}
      {list.length === 0 && locked ? null : list.length === 0 ? (
        <p className="rounded-lg border border-dashed px-4 py-10 text-center text-sm text-muted-foreground">
          This app has no backups yet. Add one here to copy a database file or folders to one of this server's storages, or declare
          them in the compose file under <code className="font-mono">x-winterflow.backups</code>.
        </p>
      ) : (
        <RowList label="Backups">
          {list.map((b) => (
            <BackupRow
              key={b.name}
              backup={b}
              health={healthOf(b.name)}
              busy={busy !== null || !!restoring || locked}
              activity={activity[b.name]}
              onSetUp={(el) => show({ kind: "enable", name: b.name }, el)}
              onRun={() => void runNow(b.name)}
              onPoints={(el) => show({ kind: "points", name: b.name }, el)}
              onEdit={(el) => show({ kind: "edit", name: b.name }, el)}
            />
          ))}
        </RowList>
      )}
      {(open?.kind === "enable" || open?.kind === "storage") && current ? (
        <BackupStorageDialog
          key={openKey}
          backup={current.name}
          current={current.storage}
          turnOn={open.kind === "enable"}
          open
          busy={busy !== null}
          error={saveError}
          returnFocus={opener}
          load={backups.serverStorages}
          onOpenChange={(o) => !o && setOpen(null)}
          onPick={(storage) =>
            open.kind === "enable"
              ? void save(current.name, { ...configOf(current), enabled: true, storage }, `${current.name} turned on`, { kind: "on" })
              : void save(current.name, { ...configOf(current), storage }, `${current.name} storage changed`)
          }
        />
      ) : null}
      {open?.kind === "edit" && current ? (
        <BackupEditDialog
          key={openKey}
          backup={current}
          retentionDays={detail?.days ?? 0}
          targets={detail?.targets ?? null}
          open
          busy={busy !== null}
          error={saveError}
          returnFocus={opener}
          onOpenChange={(o) => !o && setOpen(null)}
          onSave={(entry) =>
            void save(
              current.name,
              entry,
              `${current.name} saved`,
              !!entry.enabled === current.enabled ? undefined : { kind: entry.enabled ? "on" : "off" },
            )
          }
          onRemove={current.in_recipe ? undefined : () => void save(current.name, null, `${current.name} removed`)}
          onChangeStorage={() => show({ kind: current.storage ? "storage" : "enable", name: current.name }, opener.current)}
        />
      ) : null}
      {open?.kind === "points" && current ? (
        <RestorePointsDialog
          key={openKey}
          appId={appId}
          backup={current}
          backups={backups}
          open
          busy={busy !== null}
          running={healthOf(current.name)?.state === "running"}
          returnFocus={opener}
          onOpenChange={(o) => !o && setOpen(null)}
          onRestored={() => void load()}
        />
      ) : null}
      {open?.kind === "add" ? (
        <AddBackupDialog
          key={openKey}
          targets={detail?.targets ?? null}
          taken={list.map((b) => b.name)}
          open
          busy={busy !== null}
          error={saveError}
          returnFocus={opener}
          load={backups.serverStorages}
          onOpenChange={(o) => !o && setOpen(null)}
          onAdd={(entry) => void save(entry.name, entry, `${entry.name} added`, { kind: "on" })}
        />
      ) : null}
    </div>
  );
}

const TYPE_LABELS: Record<EffectiveBackup["type"], string> = { sqlite: "SQLite database", files: "Folders", custom: "Custom" };

// BackupRow is one backup as a flat row: its name with a muted line (type,
// location, storage, schedule), its state with the last and next copy, and
// its actions in a ⋯ menu: Set up (no storage yet; also inline), Back up now
// and Restore (built-in backups only: winterflow neither runs nor lists a
// custom backup's copies), Edit (on/off is there).
function BackupRow({
  backup: b,
  health,
  busy,
  activity,
  onSetUp,
  onRun,
  onPoints,
  onEdit,
}: {
  backup: EffectiveBackup;
  health: BackupHealth | undefined;
  busy: boolean;
  activity: readonly Activity[] | undefined;
  onSetUp: (trigger: HTMLElement) => void;
  onRun: () => void;
  onPoints: (trigger: HTMLElement) => void;
  onEdit: (trigger: HTMLElement) => void;
}) {
  const menuRef = useRef<HTMLButtonElement>(null);
  const builtin = b.type !== "custom";
  const kinds = activity ?? [];
  const { state, restoring: restoringRow, running, note } = backupRowState(b, health, kinds);
  const where = [TYPE_LABELS[b.type], locationLabel(b)].filter(Boolean).join(" · ");
  const detail = [b.storage ? `${where} → ${b.storage}` : where, builtin ? scheduleLabel(b.every) : "Its own tool"].join(" · ");
  const runDisabled = !b.enabled || running || busy;
  return (
    <li className="flex flex-col gap-2 px-4 py-3 transition-colors duration-150 hover:bg-muted/40 max-sm:rounded-xl max-sm:border max-sm:bg-card sm:flex-row sm:items-center sm:gap-4">
      <div className="min-w-0 flex-1">
        <span className="font-mono text-sm font-medium">{b.name}</span>
        <div className="mt-0.5 text-xs text-muted-foreground [overflow-wrap:anywhere]">{detail}</div>
        {state === "failing" && health?.last_error ? <RowError error={health.last_error} /> : null}
      </div>
      <div className="flex flex-wrap items-center gap-x-2 gap-y-1 sm:w-60 sm:justify-end">
        <span title={health?.last_error}>
          <StatusPill status={restoringRow ? RESTORING : backupStatus(state)} />
        </span>
        {health?.last_success ? (
          <span className="text-xs text-muted-foreground">Last copy {relativeTime(new Date(health.last_success * 1000))}</span>
        ) : null}
        {note ? <span className="w-full text-xs text-muted-foreground sm:text-right">{note}</span> : null}
      </div>
      <div className="flex shrink-0 items-center justify-end gap-1">
        {b.storage ? null : (
          <Button
            size="sm"
            variant="outline"
            className="max-sm:h-10"
            disabled={busy}
            aria-label={`Set up ${b.name}`}
            onClick={(e) => onSetUp(e.currentTarget)}
          >
            Set up…
          </Button>
        )}
        <DropdownMenu modal={false}>
          <DropdownMenuTrigger asChild>
            <Button
              ref={menuRef}
              size="icon"
              variant="ghost"
              className="size-8 text-muted-foreground max-sm:size-10"
              aria-label={`Actions for ${b.name}`}
            >
              <MoreHorizontal className="size-4" />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            {b.storage ? null : (
              <DropdownMenuItem disabled={busy} onSelect={() => menuRef.current && onSetUp(menuRef.current)}>
                Set up…
              </DropdownMenuItem>
            )}
            {builtin ? (
              <>
                <DropdownMenuItem disabled={runDisabled} onSelect={onRun}>
                  Back up now
                </DropdownMenuItem>
                <DropdownMenuItem
                  disabled={!b.storage || busy}
                  onSelect={() => menuRef.current && onPoints(menuRef.current)}
                >
                  <span className="grid">
                    Restore…
                    {b.storage ? null : <span className="text-xs text-muted-foreground">Set up the backup first: its copies are kept in its storage</span>}
                  </span>
                </DropdownMenuItem>
              </>
            ) : null}
            <DropdownMenuItem disabled={busy} onSelect={() => menuRef.current && onEdit(menuRef.current)}>
              Edit…
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
    </li>
  );
}

// RowError is a failing backup's error in a line, with the agent's full text
// (paths, temporary names) one click away.
function RowError({ error }: { error: string }) {
  const summary = errorSummary(error);
  const more = summary !== error.trim();
  return (
    <div className="mt-1 text-xs text-destructive [overflow-wrap:anywhere]">
      <span>{summary}</span>
      {more ? (
        <details className="mt-0.5">
          <summary className="w-fit cursor-pointer text-muted-foreground hover:text-foreground">Details</summary>
          <p className="mt-1 whitespace-pre-wrap">{error}</p>
        </details>
      ) : null}
    </div>
  );
}
