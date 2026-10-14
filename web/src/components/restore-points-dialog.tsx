import { useCallback, useEffect, useRef, useState, type RefObject } from "react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { InlineError } from "@/components/inline-error";
import { ResponsiveDialog } from "@/components/responsive-dialog";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import type { Backups } from "@/context/use-backups";
import { useConfirmTarget } from "@/hooks/use-confirm-target";
import { isRunning, restoringBackup, trackActivity, useActivity } from "@/lib/backup-activity";
import { expiresNote, formatSize, KIND_LABELS, pointLabel, type EffectiveBackup, type RestorePoint } from "@/lib/backups";
import { returnFocusTo } from "@/lib/focus";
import { relativeTime } from "@/lib/utils";

type Confirm = { action: "restore" | "delete"; point: RestorePoint };

const when = (p: RestorePoint) => new Date(p.time * 1000).toLocaleString();

// RestorePointsDialog lists a backup's copies, each restorable (after a
// confirmation: the app stops and its current data is first saved as a
// pre-restore copy) and deletable. A backup that is off can't be restored
// (its storage and mounts are only resolved while it is on). A restore can
// run for a long while: the dialog may be closed meanwhile, the page shows
// it under way (backup-activity) and a toast reports the end. The parent
// remounts it per opening.
export function RestorePointsDialog({
  appId,
  backup,
  backups,
  open,
  busy: saving = false,
  running = false,
  returnFocus,
  onOpenChange,
  onRestored,
}: {
  appId: string;
  backup: EffectiveBackup;
  backups: Backups;
  open: boolean;
  busy?: boolean; // a change to the app's backups is being saved
  running?: boolean; // the agent reports a run of this backup
  returnFocus?: RefObject<HTMLElement | null>; // where focus goes back when the dialog closes
  onOpenChange: (open: boolean) => void;
  onRestored?: () => void;
}) {
  const [points, setPoints] = useState<RestorePoint[] | null>(null);
  const [error, setError] = useState("");
  const confirm = useConfirmTarget<Confirm>();
  const [busy, setBusy] = useState<"restore" | "delete" | null>(null);
  const [failure, setFailure] = useState("");
  // The row button that opened the confirmation, and the footer's Close:
  // focus goes back to the first one still on the page.
  const opener = useRef<HTMLElement | null>(null);
  const closeButton = useRef<HTMLButtonElement>(null);
  // Any restore of this app, started here or before (a restore stops the app,
  // and the agent refuses a second one).
  const activity = useActivity(appId);
  const restoring = restoringBackup(activity) !== undefined;
  // A run of this backup (queued ahead or not) or a save in flight: the
  // agent would hold a restore behind them, so it isn't offered.
  const runBusy = isRunning(activity, backup.name, "backup") || running || saving;

  const load = useCallback(async () => {
    setError("");
    try {
      setPoints(await backups.listRestorePoints(appId, backup.name));
    } catch (e) {
      setError(e instanceof Error ? e.message : "The copies could not be listed.");
    }
  }, [appId, backup.name, backups]);

  useEffect(() => {
    if (open) void load();
  }, [open, load]);

  // The dialog may be closed while a restore runs: its end then only toasts.
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);

  const run = async ({ action, point }: Confirm) => {
    setBusy(action);
    setFailure("");
    const end = action === "restore" ? trackActivity(appId, backup.name, "restore") : undefined;
    try {
      if (action === "restore") {
        const res = await backups.restoreBackup(appId, backup.name, point.id);
        const saved = res.pre_restore_point
          ? `The data it replaced was saved as a ${pointLabel(res.pre_restore_point, "pre-restore")}.`
          : "";
        toast.success(`${backup.name} restored to ${when(point)}`, {
          description: [saved, ...res.warnings].filter(Boolean).join("\n") || undefined,
        });
        onRestored?.();
        if (mounted.current) await load();
      } else {
        await backups.deleteRestorePoint(appId, backup.name, point.id);
        toast.success("Copy deleted");
        setPoints((prev) => prev && prev.filter((p) => p.id !== point.id));
      }
    } catch (e) {
      const message = e instanceof Error ? e.message : "";
      setFailure(message);
      // The restore's error names the pre-restore copy or the .wf-restore-*.old
      // folder to recover from: it stays up long enough to be read.
      toast.error(action === "restore" ? "Restore failed" : "Couldn't delete the copy", {
        description: message || undefined,
        ...(action === "restore" ? { duration: 30_000 } : {}),
      });
      throw e; // keeps the confirmation open
    } finally {
      end?.();
      setBusy(null);
    }
  };

  const what = backup.type === "sqlite" ? "database" : "files";
  const hasExclude = (backup.exclude ?? []).length > 0;
  const toggleOpener = (e: { currentTarget: HTMLElement }, next: Confirm) => {
    opener.current = e.currentTarget;
    setFailure("");
    confirm.ask(next);
  };
  const target = confirm.target;
  return (
    <>
      <ResponsiveDialog
        open={open}
        onOpenChange={onOpenChange}
        className="sm:max-w-2xl"
        onCloseAutoFocus={returnFocusTo(returnFocus)}
        title={<>Copies of {backup.name}</>}
        description={
          <>
            Stored in <code className="font-mono text-foreground">{backup.storage || "its storage"}</code> · newest first
          </>
        }
        footer={
          <Button ref={closeButton} variant="outline" onClick={() => onOpenChange(false)}>
            Close
          </Button>
        }
      >
        <div className="grid gap-4">
          {!backup.enabled ? (
            <p className="text-sm text-muted-foreground">
              Turn the backup on to restore a copy: the app's mounts and the storage are only used while it is on. You can still
              delete copies.
            </p>
          ) : restoring ? (
            <p role="status" className="text-sm text-muted-foreground">
              A restore of this app is running: copies can't be restored or deleted until it is done.
            </p>
          ) : runBusy ? (
            <p role="status" className="text-sm text-muted-foreground">
              A backup of {backup.name} is running or being saved: a copy can be restored when it is done.
            </p>
          ) : null}
          {error ? (
            <div className="grid justify-items-start gap-2">
              <InlineError className="[overflow-wrap:anywhere]">{error}</InlineError>
              <Button variant="outline" size="sm" onClick={() => void load()}>
                Try again
              </Button>
            </div>
          ) : points === null ? (
            <div className="flex h-24 items-center justify-center">
              <Spinner />
            </div>
          ) : points.length === 0 ? (
            <p className="rounded-lg border border-dashed px-4 py-8 text-center text-sm text-muted-foreground">
              No copies yet. Back up now to make one.
            </p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Copy</TableHead>
                  <TableHead className="w-px">
                    <span className="sr-only">Actions</span>
                  </TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {points.map((p) => {
                  const expires = expiresNote(p);
                  return (
                    <TableRow key={p.id}>
                      <TableCell>
                        <div>{when(p)}</div>
                        {/* Kind and size sit under the time, so the table is
                            two columns wide and the actions fit a phone. */}
                        <div className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted-foreground">
                          <Badge variant={p.kind === "pre-restore" ? "secondary" : "outline"}>{KIND_LABELS[p.kind]}</Badge>
                          <span>{formatSize(p.size)}</span>
                          <span>{relativeTime(new Date(p.time * 1000))}</span>
                          {expires ? <span className="font-medium text-foreground">{expires}</span> : null}
                        </div>
                      </TableCell>
                      <TableCell className="whitespace-nowrap py-0.5 text-right">
                        <Button
                          size="sm"
                          variant="ghost"
                          aria-label={`Restore the copy of ${when(p)}`}
                          disabled={busy !== null || !backup.enabled || restoring || runBusy}
                          title={backup.enabled ? undefined : "Turn the backup on to restore"}
                          onClick={(e) => toggleOpener(e, { action: "restore", point: p })}
                        >
                          Restore
                        </Button>
                        <Button
                          size="sm"
                          variant="ghost"
                          className="text-destructive hover:text-destructive"
                          aria-label={`Delete the copy of ${when(p)}`}
                          disabled={busy !== null || restoring}
                          onClick={(e) => toggleOpener(e, { action: "delete", point: p })}
                        >
                          Delete
                        </Button>
                      </TableCell>
                    </TableRow>
                  );
                })}
              </TableBody>
            </Table>
          )}
        </div>
      </ResponsiveDialog>
      <ConfirmDialog
        open={confirm.open}
        onOpenChange={confirm.onOpenChange}
        onCloseAutoFocus={returnFocusTo(opener, closeButton)}
        title={target?.action === "delete" ? "Delete this copy?" : "Restore this copy?"}
        description={
          target === null ? undefined : target.action === "restore" ? (
            <>
              Restore {backup.name} to the copy of <strong>{when(target.point)}</strong>.
            </>
          ) : (
            <>
              Delete the copy of <strong>{when(target.point)}</strong> ({formatSize(target.point.size)})? It can't be restored
              afterwards.
              {target.point.kind === "pre-restore"
                ? " This is the data a restore replaced: it may be the only way back from that restore."
                : ""}
            </>
          )
        }
        confirmLabel={target?.action === "delete" ? "Delete copy" : "Restore"}
        pendingLabel={target?.action === "delete" ? "Deleting…" : "Restoring…"}
        // A restore can run long: its confirmation may be closed meanwhile.
        pendingCancelLabel={target?.action === "restore" ? "Close" : undefined}
        onConfirm={() => (target ? run(target) : undefined)}
      >
        {target?.action === "restore" ? (
          <ul className="list-disc space-y-1 pl-5 text-sm text-muted-foreground">
            <li>
              The app stops while its {what} {what === "database" ? "is" : "are"} replaced, then starts again if it was running.
            </li>
            <li>
              The current {what} {what === "database" ? "is" : "are"} first saved as a <strong>pre-restore</strong> copy, so you
              can go back.
            </li>
            {what === "files" ? <li>Files added to these paths since this copy was made are removed.</li> : null}
            {hasExclude ? <li>Files this backup excludes are kept as they are.</li> : null}
            <li>It can take a while for a large backup. You can close this window; the result shows up as a notification.</li>
          </ul>
        ) : null}
        {busy === "restore" ? (
          <p role="status" className="flex items-center gap-2 text-sm text-muted-foreground">
            <Spinner aria-hidden className="size-4" /> Restoring: the app is stopped until it is done.
          </p>
        ) : null}
        {failure ? <InlineError className="[overflow-wrap:anywhere]">{failure}</InlineError> : null}
      </ConfirmDialog>
    </>
  );
}
