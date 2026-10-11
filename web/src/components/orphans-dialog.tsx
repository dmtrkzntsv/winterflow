import { useCallback, useEffect, useState, type RefObject } from "react";
import { toast } from "sonner";

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
import { formatBytes, relativeAge, type Orphan } from "@/lib/backups";
import { returnFocusTo } from "@/lib/focus";
import { cn } from "@/lib/utils";

const day = (unix: number) => new Date(unix * 1000).toLocaleDateString();

// purgeLabel says when the daily sweep deletes an orphan's last copy: a
// deleted app's folder goes at the retention limit; another server's is
// never deleted automatically.
function purgeLabel(o: Orphan, now: number): string {
  if (!o.deleted_at) return "Not deleted automatically (another server's?)";
  if (!o.purge_at) return "Kept: no retention limit on this server";
  return o.purge_at * 1000 <= now ? "At the next daily sweep" : `Deleted on ${day(o.purge_at)}`;
}

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
      toast.success(`Backups of ${o.app_name || o.app_id} deleted`, { description: `${n} objects removed from ${storage}.` });
      setItems((prev) => prev && prev.filter((x) => x.app_id !== o.app_id));
      setConfirm(null);
    } catch (e) {
      toast.error("Couldn't delete the backups", { description: e instanceof Error ? e.message : undefined });
    } finally {
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
        {confirm ? (
          <div role="alertdialog" aria-labelledby="orphan-confirm" className="grid gap-3 rounded-md border border-destructive/40 bg-destructive/5 p-4">
            <p id="orphan-confirm" className="text-sm">
              Delete every copy of <strong>{confirm.app_name || confirm.app_id}</strong> ({confirm.copies}{" "}
              {confirm.copies === 1 ? "copy" : "copies"}, {formatBytes(confirm.bytes)}) from {storage}? This cannot be undone.
            </p>
            <div className="flex justify-end gap-2">
              <Button variant="outline" size="sm" onClick={() => setConfirm(null)} disabled={busy}>
                Cancel
              </Button>
              <Button size="sm" className={buttonVariants({ variant: "destructive", size: "sm" })} onClick={() => void remove(confirm)} disabled={busy}>
                {busy ? "Deleting…" : "Delete backups"}
              </Button>
            </div>
          </div>
        ) : null}
        {error ? (
          <p role="alert" className="text-sm text-destructive">
            {error}
          </p>
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
                <TableHead>Deleted</TableHead>
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
                  </TableCell>
                  <TableCell className="hidden sm:table-cell">
                    {o.copies} · {formatBytes(o.bytes)}
                  </TableCell>
                  <TableCell className="hidden sm:table-cell">{o.newest ? relativeAge(o.newest, now) : "—"}</TableCell>
                  <TableCell className={cn("text-sm", !o.deleted_at && "text-muted-foreground")}>{purgeLabel(o, now)}</TableCell>
                  <TableCell className="py-0.5 text-right">
                    <Button
                      size="sm"
                      variant="ghost"
                      className="text-destructive hover:text-destructive"
                      aria-label={`Delete the backups of ${o.app_name || o.app_id}`}
                      onClick={() => setConfirm(o)}
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
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            Close
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
