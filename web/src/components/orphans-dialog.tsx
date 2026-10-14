import { useEffect, useRef, useState, type RefObject } from "react";
import { toast } from "sonner";

import { ConfirmDialog } from "@/components/confirm-dialog";
import { EmptyState } from "@/components/empty-state";
import { InlineError } from "@/components/inline-error";
import { ResponsiveDialog } from "@/components/responsive-dialog";
import { Row, RowList } from "@/components/section";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import type { Backups } from "@/context/use-backups";
import { useNow } from "@/hooks/use-now";
import { useConfirmTarget } from "@/hooks/use-confirm-target";
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

// shownName is how an orphan is named on screen: its app's name, never its
// raw id (that one sits in a title, for hover).
const shownName = (o: Orphan) => o.app_name || "an unknown app";

// OrphansDialog lists the backups in a storage that belong to no app on
// this server: a deleted app's (named from its marker) or another server's
// sharing the storage, each deletable after a confirmation. The parent
// remounts it (key) per opening; returnFocus is where focus goes back on
// close.
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
  const confirm = useConfirmTarget<Orphan>();
  const now = useNow();
  // The Delete button that opened the confirmation, and the footer's Close
  // button: focus goes back to the first one still on the page.
  const opener = useRef<HTMLElement | null>(null);
  const closeButton = useRef<HTMLButtonElement>(null);

  // Another listing is asked for by bumping the attempt (Try again).
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    if (!open) return;
    let live = true;
    backups.listOrphans(storage).then(
      (list) => {
        if (!live) return;
        setItems(list);
        setError("");
      },
      (e) => {
        if (live) setError(e instanceof Error ? e.message : "The storage could not be listed.");
      },
    );
    return () => {
      live = false;
    };
  }, [open, backups, storage, attempt]);

  const remove = async (o: Orphan) => {
    try {
      const n = await backups.deleteOrphan(storage, o.app_id);
      toast.success(`Backups of ${shownName(o)} deleted`, {
        description: `${n} ${n === 1 ? "object" : "objects"} removed from ${storage}.`,
      });
      setItems((prev) => prev && prev.filter((x) => x.app_id !== o.app_id));
    } catch (e) {
      toast.error("Couldn't delete the backups", { description: e instanceof Error ? e.message : undefined });
      throw e;
    }
  };

  return (
    <ResponsiveDialog
      open={open}
      onOpenChange={onOpenChange}
      title="Backups of deleted apps"
      description={
        <>
          In <code className="font-mono text-foreground">{storage}</code>: copies of apps deleted from this server, or of
          another server's apps sharing this storage.
        </>
      }
      className="sm:max-w-2xl"
      onCloseAutoFocus={(e) => returnFocusTo(returnFocus)(e)}
      footer={
        <Button ref={closeButton} variant="outline" onClick={() => onOpenChange(false)}>
          Close
        </Button>
      }
    >
      {error ? (
        <div className="grid justify-items-start gap-2">
          <InlineError>{error}</InlineError>
          <Button variant="outline" size="sm" onClick={() => setAttempt((n) => n + 1)}>
            Try again
          </Button>
        </div>
      ) : items === null ? (
        <div className="flex h-24 items-center justify-center">
          <Spinner />
        </div>
      ) : items.length === 0 ? (
        <EmptyState title="Every backup in this storage belongs to an app on this server." />
      ) : (
        <RowList label="Backups of deleted apps">
          {items.map((o) => (
            <Row
              key={o.app_id}
              title={<span title={o.app_id}>{o.app_name || "Unknown app"}</span>}
              description={[
                copiesLabel(o.copies),
                formatSize(o.bytes),
                o.newest ? relativeTime(new Date(o.newest * 1000), now) : "",
              ]
                .filter(Boolean)
                .join(" · ")}
              meta={
                <span className={cn(!o.deleted_at && "text-muted-foreground")}>
                  {o.deleted_at ? `Deleted ${day(o.deleted_at)} · ` : ""}
                  {purgeLabel(o, now)}
                </span>
              }
              actions={
                <Button
                  size="sm"
                  variant="ghost"
                  className="text-destructive hover:text-destructive"
                  aria-label={`Delete the backups of ${o.app_name || o.app_id}`}
                  onClick={(e) => {
                    opener.current = e.currentTarget;
                    confirm.ask(o);
                  }}
                >
                  Delete
                </Button>
              }
            />
          ))}
        </RowList>
      )}
      {/* A dialog of its own, so it is on screen however far the list is
          scrolled, takes focus (on Cancel), and Escape closes only it. */}
      <ConfirmDialog
        open={confirm.open}
        onOpenChange={confirm.onOpenChange}
        title="Delete these backups?"
        description={
          confirm.target ? (
            confirm.target.deleted_at ? (
              <>
                Delete every copy of <strong>{shownName(confirm.target)}</strong> ({copiesLabel(confirm.target.copies)},{" "}
                {formatSize(confirm.target.bytes)}) from {storage}? This cannot be undone.
              </>
            ) : (
              <>
                This folder is not marked as deleted by this server, so it may hold the live backups of an app on another
                server that shares this storage. Deleting every copy of <strong>{shownName(confirm.target)}</strong> (
                {copiesLabel(confirm.target.copies)}, {formatSize(confirm.target.bytes)}) from {storage} is permanent, and that app
                would lose its backups. Delete only if you are sure nothing uses them.
              </>
            )
          ) : null
        }
        confirmLabel="Delete backups"
        pendingLabel="Deleting…"
        onCloseAutoFocus={(e) => returnFocusTo(opener, closeButton)(e)}
        onConfirm={() => (confirm.target ? remove(confirm.target) : undefined)}
      />
    </ResponsiveDialog>
  );
}
