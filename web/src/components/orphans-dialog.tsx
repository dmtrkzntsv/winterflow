import { useCallback, useEffect, useRef, useState, type RefObject } from "react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { buttonVariants } from "@/components/ui/button-variants";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Spinner } from "@/components/ui/spinner";
import { InlineError } from "@/components/inline-error";
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
import { formatSize, type Orphan } from "@/lib/backups";
import { returnFocusTo } from "@/lib/focus";
import { cn, relativeTime } from "@/lib/utils";

const day = (unix: number) => new Date(unix * 1000).toLocaleDateString();

// purgeLabel says when the daily sweep deletes an orphan's last copy: a
// deleted app's folder goes at the retention limit; another server's is
// never deleted automatically.
function purgeLabel(o: Orphan, now: number): string {
  if (!o.deleted_at) return "Not deleted automatically (another server's?)";
  if (!o.purge_at) return "Kept: no retention limit on this server";
  return o.purge_at * 1000 <= now ? "At the next daily sweep" : `Will be deleted on ${day(o.purge_at)}`;
}

const copiesLabel = (n: number) => `${n} ${n === 1 ? "copy" : "copies"}`;

// OrphansDialog lists a storage's "Backups of apps not on this server": a
// deleted app's (named from its marker) or another server's sharing the
// storage, each deletable after a confirmation. The parent remounts it
// (key) per opening; returnFocus is where focus goes back on close.
export function OrphansDialog({
  backups,
  storage,
  open,
  returnFocus,
  onOpenChange,
}: {
  backups: Backups;
  storage: string;
  open: boolean;
  returnFocus?: RefObject<HTMLElement | null>;
  onOpenChange: (open: boolean) => void;
}) {
  const [items, setItems] = useState<Orphan[] | null>(null);
  const [error, setError] = useState("");
  const [confirm, setConfirm] = useState<Orphan | null>(null);
  const [busy, setBusy] = useState(false);
  // The Delete button that opened the confirmation, and the footer's Close
  // button: focus goes back to the first one still on the page.
  const opener = useRef<HTMLElement | null>(null);
  const closeButton = useRef<HTMLButtonElement>(null);

  const load = useCallback(async () => {
    setError("");
    try {
      setItems(await backups.listOrphans(storage));
    } catch (e) {
      setError(e instanceof Error ? e.message : "The storage could not be listed.");
    }
  }, [backups, storage]);

  useEffect(() => {
    if (open) void load();
  }, [open, load]);

  const remove = async (o: Orphan) => {
    setBusy(true);
    try {
      const n = await backups.deleteOrphan(storage, o.app_id);
      toast.success(`Backups of ${o.app_name || o.app_id} deleted`, {
        description: `${n} ${n === 1 ? "object" : "objects"} removed from ${storage}.`,
      });
      setItems((prev) => prev && prev.filter((x) => x.app_id !== o.app_id));
    } catch (e) {
      toast.error("Couldn't delete the backups", { description: e instanceof Error ? e.message : undefined });
    } finally {
      setConfirm(null);
      setBusy(false);
    }
  };

  const now = Date.now();
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[calc(100dvh-2rem)] overflow-y-auto sm:max-w-3xl" onCloseAutoFocus={returnFocusTo(returnFocus)}>
        <DialogHeader>
          <DialogTitle>Backups of apps not on this server</DialogTitle>
          <DialogDescription>
            In <code className="font-mono text-foreground">{storage}</code>: copies of apps deleted here, or of another
            server's apps sharing this storage.
          </DialogDescription>
        </DialogHeader>
        {error ? (
          <div className="grid justify-items-start gap-2">
            <InlineError>{error}</InlineError>
            <Button variant="outline" size="sm" onClick={() => void load()}>
              Try again
            </Button>
          </div>
        ) : items === null ? (
          <div className="flex h-24 items-center justify-center">
            <Spinner />
          </div>
        ) : items.length === 0 ? (
          <p className="rounded-lg border border-dashed px-4 py-8 text-center text-sm text-muted-foreground">
            Every backup in this storage belongs to an app on this server.
          </p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>App</TableHead>
                <TableHead className="hidden sm:table-cell">Copies</TableHead>
                <TableHead className="hidden sm:table-cell">Newest</TableHead>
                <TableHead>Automatic deletion</TableHead>
                <TableHead className="w-px">
                  <span className="sr-only">Actions</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.map((o) => (
                <TableRow key={o.app_id}>
                  <TableCell>
                    <div className="font-medium [overflow-wrap:anywhere]">{o.app_name || "Unknown app"}</div>
                    <div className="font-mono text-xs text-muted-foreground [overflow-wrap:anywhere]">{o.app_id}</div>
                    {o.deleted_at ? <div className="text-xs text-muted-foreground">Deleted {day(o.deleted_at)}</div> : null}
                    <div className="text-xs text-muted-foreground sm:hidden">
                      {copiesLabel(o.copies)} · {formatSize(o.bytes)}
                      {o.newest ? ` · ${relativeTime(new Date(o.newest * 1000), now)}` : ""}
                    </div>
                  </TableCell>
                  <TableCell className="hidden sm:table-cell">
                    {o.copies} · {formatSize(o.bytes)}
                  </TableCell>
                  <TableCell className="hidden sm:table-cell">{o.newest ? relativeTime(new Date(o.newest * 1000), now) : "—"}</TableCell>
                  <TableCell className={cn("text-sm", !o.deleted_at && "text-muted-foreground")}>{purgeLabel(o, now)}</TableCell>
                  <TableCell className="py-0.5 text-right">
                    <Button
                      size="sm"
                      variant="ghost"
                      className="text-destructive hover:text-destructive"
                      aria-label={`Delete the backups of ${o.app_name || o.app_id}`}
                      onClick={(e) => {
                        opener.current = e.currentTarget;
                        setConfirm(o);
                      }}
                      disabled={busy}
                    >
                      Delete
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
        <DialogFooter>
          <Button ref={closeButton} variant="outline" onClick={() => onOpenChange(false)}>
            Close
          </Button>
        </DialogFooter>
      </DialogContent>
      {/* A dialog of its own, so it is on screen however far the list is
          scrolled, takes focus (on Cancel), and Escape closes only it. */}
      <AlertDialog open={confirm !== null} onOpenChange={(o) => !o && !busy && setConfirm(null)}>
        {confirm ? (
          <AlertDialogContent onCloseAutoFocus={returnFocusTo(opener, closeButton)}>
            <AlertDialogHeader>
              <AlertDialogTitle>Delete these backups?</AlertDialogTitle>
              <AlertDialogDescription>
                {confirm.deleted_at ? (
                  <>
                    Delete every copy of <strong>{confirm.app_name || confirm.app_id}</strong> (
                    {copiesLabel(confirm.copies)}, {formatSize(confirm.bytes)}) from {storage}? This cannot be undone.
                  </>
                ) : (
                  <>
                    This folder is not marked as deleted by this server, so it may hold the live backups of an app on
                    another server that shares this storage. Deleting every copy of{" "}
                    <strong>{confirm.app_name || confirm.app_id}</strong> ({copiesLabel(confirm.copies)},{" "}
                    {formatSize(confirm.bytes)}) from {storage} is permanent, and that app would lose its backups. Delete
                    only if you are sure nothing uses them.
                  </>
                )}
              </AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel disabled={busy}>Cancel</AlertDialogCancel>
              <AlertDialogAction
                className={buttonVariants({ variant: "destructive" })}
                disabled={busy}
                onClick={(e) => {
                  e.preventDefault();
                  void remove(confirm);
                }}
              >
                {busy ? "Deleting…" : "Delete backups"}
              </AlertDialogAction>
            </AlertDialogFooter>
          </AlertDialogContent>
        ) : null}
      </AlertDialog>
    </Dialog>
  );
}
