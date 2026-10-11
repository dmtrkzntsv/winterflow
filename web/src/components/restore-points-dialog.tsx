import { useCallback, useEffect, useRef, useState } from "react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { buttonVariants } from "@/components/ui/button-variants";
import { Spinner } from "@/components/ui/spinner";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import type { Backups } from "@/context/use-backups";
import { trackActivity, useActivity } from "@/lib/backup-activity";
import { expiresNote, formatBytes, KIND_LABELS, relativeAge, type EffectiveBackup, type RestorePoint } from "@/lib/backups";

type Confirm = { action: "restore" | "delete"; point: RestorePoint };

const when = (p: RestorePoint) => new Date(p.time * 1000).toLocaleString();

// RestorePointsDialog lists a backup's copies (time, kind, size; "expires
// in N days" near the server's ceiling), each restorable — after a
// confirmation saying the app stops and its current data is first saved as
// a pre-restore point — and deletable. A backup that is off can't be
// restored (its storage and mounts are only resolved while it is on).
// A restore can run for a long while (its result arrives over SSE): the
// dialog may be closed meanwhile, the page shows the restore under way
// (backup-activity), and the toast reports the end. onRestored runs after a
// restore (the app's status changed). The parent remounts it per opening.
export function RestorePointsDialog({
  appId,
  backup,
  backups,
  open,
  onOpenChange,
  onRestored,
}: {
  appId: string;
  backup: EffectiveBackup;
  backups: Backups;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onRestored?: () => void;
}) {
  const [points, setPoints] = useState<RestorePoint[] | null>(null);
  const [error, setError] = useState("");
  const [confirm, setConfirm] = useState<Confirm | null>(null);
  const [busy, setBusy] = useState<"restore" | "delete" | null>(null);
  const [failure, setFailure] = useState("");
  // Any restore of this app, started here or before (a restore stops the app,
  // and the agent refuses a second one).
  const restoring = Object.values(useActivity(appId)).includes("restore");

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
        const saved = res.pre_restore_point ? `The data it replaced was saved as ${res.pre_restore_point}.` : "";
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
      setConfirm(null);
    } catch (e) {
      const message = e instanceof Error ? e.message : "";
      setFailure(message);
      // The restore's error names the pre-restore copy or the .wf-restore-*.old
      // folder to recover from: it stays up long enough to be read.
      toast.error(action === "restore" ? "Restore failed" : "Couldn't delete the copy", {
        description: message || undefined,
        ...(action === "restore" ? { duration: 30_000 } : {}),
      });
    } finally {
      end?.();
      setBusy(null);
    }
  };

  const what = backup.type === "sqlite" ? "database" : "files";
  const hasExclude = (backup.exclude ?? []).length > 0;
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[calc(100dvh-2rem)] overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>Copies of {backup.name}</DialogTitle>
          <DialogDescription>Newest first, in {backup.storage || "its storage"}.</DialogDescription>
        </DialogHeader>
        {confirm ? (
          <div
            role="alertdialog"
            aria-labelledby="point-confirm-title"
            aria-describedby="point-confirm"
            className="grid gap-3 rounded-md border border-destructive/40 bg-destructive/5 p-4"
          >
            <h3 id="point-confirm-title" className="text-sm font-medium">
              {confirm.action === "restore" ? "Restore this copy?" : "Delete this copy?"}
            </h3>
            {confirm.action === "restore" ? (
              <div id="point-confirm" className="grid gap-2 text-sm">
                <p>
                  Restore {backup.name} to the copy of <strong>{when(confirm.point)}</strong>.
                </p>
                <ul className="list-disc space-y-1 pl-5">
                  <li>
                    The app stops while its {what} {what === "database" ? "is" : "are"} replaced, then starts again if it was running.
                  </li>
                  <li>
                    The current {what} {what === "database" ? "is" : "are"} first saved as a <strong>pre-restore</strong> copy, so
                    you can go back.
                  </li>
                  {hasExclude ? <li>Files this backup excludes are kept as they are.</li> : null}
                  <li>It can take a while for a large backup. You can close this window; the result shows up as a notification.</li>
                </ul>
              </div>
            ) : (
              <p id="point-confirm" className="text-sm">
                Delete the copy of <strong>{when(confirm.point)}</strong> ({formatBytes(confirm.point.size)})? It can't be restored
                afterwards.
              </p>
            )}
            {busy === "restore" ? (
              <p role="status" className="flex items-center gap-2 text-sm text-muted-foreground">
                <Spinner aria-hidden className="size-4" /> Restoring: the app is stopped until it is done.
              </p>
            ) : null}
            {failure ? (
              <p role="alert" className="text-sm text-destructive [overflow-wrap:anywhere]">
                {failure}
              </p>
            ) : null}
            <div className="flex justify-end gap-2">
              <Button variant="outline" size="sm" autoFocus onClick={() => setConfirm(null)} disabled={busy !== null}>
                Cancel
              </Button>
              <Button size="sm" className={buttonVariants({ variant: "destructive", size: "sm" })} onClick={() => void run(confirm)} disabled={busy !== null}>
                {confirm.action === "restore" ? (busy ? "Restoring…" : "Restore") : busy ? "Deleting…" : "Delete copy"}
              </Button>
            </div>
          </div>
        ) : null}
        {!backup.enabled ? (
          <p className="text-sm text-muted-foreground">
            Turn the backup on to restore a copy: the app's mounts and the storage are only used while it is on. You can still
            delete copies.
          </p>
        ) : restoring && busy !== "restore" ? (
          <p role="status" className="text-sm text-muted-foreground">
            A restore of this app is running; another can start when it is done.
          </p>
        ) : null}
        {error ? (
          <p role="alert" className="text-sm text-destructive">
            {error}
          </p>
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
                <TableHead>Time</TableHead>
                <TableHead>Kind</TableHead>
                <TableHead className="hidden sm:table-cell">Size</TableHead>
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
                      <div className="text-xs text-muted-foreground">
                        {relativeAge(p.time)}
                        {expires ? <span className="text-amber-700 dark:text-amber-400"> · {expires}</span> : null}
                      </div>
                    </TableCell>
                    <TableCell>
                      <Badge variant={p.kind === "pre-restore" ? "secondary" : "outline"}>{KIND_LABELS[p.kind]}</Badge>
                    </TableCell>
                    <TableCell className="hidden sm:table-cell">{formatBytes(p.size)}</TableCell>
                    <TableCell className="whitespace-nowrap py-0.5 text-right">
                      <Button
                        size="sm"
                        variant="ghost"
                        aria-label={`Restore the copy of ${when(p)}`}
                        disabled={busy !== null || !backup.enabled || restoring}
                        title={backup.enabled ? undefined : "Turn the backup on to restore"}
                        onClick={() => setConfirm({ action: "restore", point: p })}
                      >
                        Restore
                      </Button>
                      <Button
                        size="sm"
                        variant="ghost"
                        className="text-destructive hover:text-destructive"
                        aria-label={`Delete the copy of ${when(p)}`}
                        disabled={busy !== null}
                        onClick={() => setConfirm({ action: "delete", point: p })}
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
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            Close
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
