import { useCallback, useEffect, useState } from "react";
import { Archive, HardDriveDownload, Pencil, Plus, RefreshCw } from "lucide-react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { Switch } from "@/components/ui/switch";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { AddBackupDialog } from "@/components/add-backup-dialog";
import { BackupEditDialog } from "@/components/backup-edit-dialog";
import { BackupStorageDialog } from "@/components/backup-storage-dialog";
import { RestorePointsDialog } from "@/components/restore-points-dialog";
import { useApps } from "@/context/use-apps";
import { trackActivity, useActivity, type Activity } from "@/lib/backup-activity";
import { useBackups } from "@/context/use-backups";
import {
  configOf,
  configsWith,
  locationLabel,
  relativeAge,
  scheduleLabel,
  STATE_LABELS,
  type BackupConfig,
  type BackupHealth,
  type BackupState,
  type BackupTargets,
  type EffectiveBackup,
} from "@/lib/backups";
import { cn } from "@/lib/utils";

const STATE_STYLE: Record<BackupState, string> = {
  ok: "border-emerald-500/40 text-emerald-700 dark:text-emerald-400",
  running: "border-sky-500/40 text-sky-700 dark:text-sky-400",
  never: "text-muted-foreground",
  off: "text-muted-foreground",
  late: "border-amber-500/50 text-amber-700 dark:text-amber-400",
  failing: "border-destructive/50 text-destructive",
};

type Detail = { list: EffectiveBackup[]; targets: BackupTargets | null; days: number; error: string };

// Open is which dialog shows, for which backup.
type Open =
  | { kind: "enable"; name: string }
  | { kind: "edit"; name: string }
  | { kind: "points"; name: string }
  | { kind: "add" }
  | null;

// AppBackups is the app page's Backups tab: each backup (declared by the
// compose file, or added here) with its storage, schedule, last copy and
// health; a switch to turn it on (picking a storage) or off; Back up now;
// its copies (restore, delete); an editor for schedule, retention and
// location; and Add backup.
export function AppBackups({ appId }: { appId: string }) {
  const { getApp, backupsByApp } = useApps();
  const backups = useBackups();
  const [detail, setDetail] = useState<Detail | null>(null);
  const [loadError, setLoadError] = useState("");
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState<string | null>(null); // the backup being saved ("" = the list)
  // What runs (Back up now, Restore) outlives this tab: see backup-activity.
  const activity = useActivity(appId);
  const restoring = Object.entries(activity).find(([, kind]) => kind === "restore")?.[0];
  const [open, setOpen] = useState<Open>(null);
  const [openKey, setOpenKey] = useState(0);

  const load = useCallback(async () => {
    setLoading(true);
    setLoadError("");
    try {
      const d = await getApp(appId);
      setDetail({ list: d.backups ?? [], targets: d.backup_targets ?? null, days: d.retention_days ?? 0, error: d.backups_error ?? "" });
    } catch (e) {
      setLoadError(e instanceof Error ? e.message : "Failed to load the backups");
    } finally {
      setLoading(false);
    }
  }, [appId, getApp]);

  useEffect(() => {
    void load();
  }, [load]);

  const show = (next: Open) => {
    setOpenKey((k) => k + 1);
    setOpen(next);
  };

  const list = detail?.list ?? [];
  const health = new Map((backupsByApp?.[appId] ?? []).map((h) => [h.name, h]));

  // save replaces the app's whole list, with name's entry replaced by entry
  // (null: removed; a new name: added).
  const save = async (name: string, entry: BackupConfig | null, done: string) => {
    setBusy(name);
    try {
      const res = await backups.updateBackups(appId, configsWith(list, name, entry));
      setDetail((d) => (d ? { ...d, list: res.backups } : d));
      setOpen(null);
      const description = [res.redeployed ? "The app was redeployed." : "", ...res.warnings].filter(Boolean).join("\n");
      if (res.warnings.length > 0) toast.warning(done, { description });
      else toast.success(done, { description: description || undefined });
    } catch (e) {
      toast.error("Couldn't save the backups", { description: e instanceof Error ? e.message : undefined });
    } finally {
      setBusy(null);
    }
  };

  const runNow = async (name: string) => {
    const end = trackActivity(appId, name, "backup");
    try {
      const res = await backups.runBackup(appId, name);
      const title = res.unchanged ? `${name}: nothing changed since the last copy` : `${name} backed up`;
      toast.success(title, { description: res.warnings.join("\n") || undefined });
    } catch (e) {
      toast.error(`Couldn't back up ${name}`, { description: e instanceof Error ? e.message : undefined });
    } finally {
      end();
    }
  };

  const toggle = (b: EffectiveBackup, on: boolean) => {
    if (on) {
      show({ kind: "enable", name: b.name });
      return;
    }
    const off = configOf(b);
    delete off.enabled;
    void save(b.name, off, `${b.name} turned off`);
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
        <p className="max-w-2xl text-sm text-muted-foreground">
          The compose file declares what can be backed up; turn a backup on to copy it to one of this server's storages on
          its schedule.
        </p>
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
          <Button size="sm" onClick={() => show({ kind: "add" })} disabled={busy !== null || !!restoring}>
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
        <p role="alert" className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive [overflow-wrap:anywhere]">
          The backups can't be read: {detail.error}
        </p>
      ) : null}
      {list.length === 0 ? (
        <p className="rounded-lg border border-dashed px-4 py-10 text-center text-sm text-muted-foreground">
          This app has no backups. Its compose file can declare them under <code className="font-mono">x-winterflow.backups</code>,
          or add one here.
        </p>
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-px">
                <span className="sr-only">On</span>
              </TableHead>
              <TableHead>Backup</TableHead>
              <TableHead className="hidden md:table-cell">Storage</TableHead>
              <TableHead className="hidden sm:table-cell">Schedule</TableHead>
              <TableHead>Last backup</TableHead>
              <TableHead className="w-px">
                <span className="sr-only">Actions</span>
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {list.map((b) => (
              <BackupRow
                key={b.name}
                backup={b}
                health={health.get(b.name)}
                busy={busy !== null || !!restoring}
                activity={activity[b.name]}
                onToggle={(on) => toggle(b, on)}
                onRun={() => void runNow(b.name)}
                onPoints={() => show({ kind: "points", name: b.name })}
                onEdit={() => show({ kind: "edit", name: b.name })}
              />
            ))}
          </TableBody>
        </Table>
      )}
      {open?.kind === "enable" && current ? (
        <BackupStorageDialog
          key={openKey}
          backup={current.name}
          current={current.storage}
          open
          load={backups.serverStorages}
          onOpenChange={(o) => !o && setOpen(null)}
          onPick={(storage) => void save(current.name, { ...configOf(current), enabled: true, storage }, `${current.name} turned on`)}
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
          onOpenChange={(o) => !o && setOpen(null)}
          onSave={(entry) => void save(current.name, entry, `${current.name} saved`)}
          onRemove={current.in_recipe ? undefined : () => void save(current.name, null, `${current.name} removed`)}
        />
      ) : null}
      {open?.kind === "points" && current ? (
        <RestorePointsDialog
          key={openKey}
          appId={appId}
          backup={current}
          backups={backups}
          open
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
          onOpenChange={(o) => !o && setOpen(null)}
          onAdd={(entry) => void save(entry.name, entry, `${entry.name} added`)}
        />
      ) : null}
    </div>
  );
}

// BackupRow is one backup: its switch, name and type, storage, schedule,
// last copy with health, and actions (built-in backups only: winterflow
// neither runs nor lists a custom backup's copies).
function BackupRow({
  backup: b,
  health,
  busy,
  activity,
  onToggle,
  onRun,
  onPoints,
  onEdit,
}: {
  backup: EffectiveBackup;
  health: BackupHealth | undefined;
  busy: boolean;
  activity: Activity | undefined;
  onToggle: (on: boolean) => void;
  onRun: () => void;
  onPoints: () => void;
  onEdit: () => void;
}) {
  const builtin = b.type !== "custom";
  const state: BackupState = activity === "backup" ? "running" : (health?.state ?? (b.enabled ? "never" : "off"));
  // A run is under way here, or the agent reports one (a page reloaded, or
  // another browser, started it): a second request would be refused.
  const running = activity === "backup" || health?.state === "running";
  return (
    <TableRow>
      <TableCell>
        <Switch checked={b.enabled} disabled={busy} aria-label={`Back up ${b.name}`} onCheckedChange={onToggle} />
      </TableCell>
      <TableCell>
        <div className="flex flex-wrap items-center gap-2">
          <span className="font-mono">{b.name}</span>
          <Badge variant="outline">{b.type}</Badge>
          {b.in_recipe ? null : <Badge variant="secondary">added here</Badge>}
        </div>
        <div className="mt-0.5 text-xs text-muted-foreground [overflow-wrap:anywhere]">{locationLabel(b)}</div>
      </TableCell>
      <TableCell className="hidden md:table-cell">{b.storage || <span className="text-muted-foreground">—</span>}</TableCell>
      <TableCell className="hidden sm:table-cell">{builtin ? scheduleLabel(b.every) : "Its own tool"}</TableCell>
      <TableCell>
        <div className="flex flex-wrap items-center gap-2">
          <Badge variant="outline" className={STATE_STYLE[activity === "restore" ? "running" : state]} title={health?.last_error}>
            {activity === "restore" ? "Restoring" : STATE_LABELS[state]}
          </Badge>
          {health?.last_success ? (
            <span className="text-xs text-muted-foreground">last backup {relativeAge(health.last_success)}</span>
          ) : null}
        </div>
        {state === "failing" && health?.last_error ? (
          <div className="mt-1 text-xs text-destructive [overflow-wrap:anywhere]">{health.last_error}</div>
        ) : null}
      </TableCell>
      <TableCell className="whitespace-nowrap py-0.5 text-right">
        {builtin ? (
          <>
            <Button
              size="icon"
              variant="ghost"
              className="size-8 text-muted-foreground"
              aria-label={`Back up ${b.name} now`}
              title="Back up now"
              disabled={!b.enabled || running || busy}
              onClick={onRun}
            >
              {running ? <Spinner className="size-4" /> : <HardDriveDownload className="size-4" />}
            </Button>
            <Button
              size="icon"
              variant="ghost"
              className="size-8 text-muted-foreground"
              aria-label={`Copies of ${b.name}`}
              title="Copies (restore, delete)"
              disabled={!b.storage}
              onClick={onPoints}
            >
              <Archive className="size-4" />
            </Button>
            <Button
              size="icon"
              variant="ghost"
              className="size-8 text-muted-foreground"
              aria-label={`Edit ${b.name}`}
              title="Edit"
              disabled={busy}
              onClick={onEdit}
            >
              <Pencil className="size-4" />
            </Button>
          </>
        ) : null}
      </TableCell>
    </TableRow>
  );
}
