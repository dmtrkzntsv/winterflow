import { useEffect, useState } from "react";
import { Link } from "react-router-dom";

import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Spinner } from "@/components/ui/spinner";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { selectClass } from "@/lib/backups";

// BackupStorageDialog turns a backup on: it picks one of the server's
// storages (load lists their names), or points to Settings → Storages when
// there are none. The parent remounts it per opening.
export function BackupStorageDialog({
  backup,
  current,
  open,
  load,
  onOpenChange,
  onPick,
}: {
  backup: string;
  current: string;
  open: boolean;
  load: () => Promise<string[]>;
  onOpenChange: (open: boolean) => void;
  onPick: (storage: string) => void;
}) {
  const [names, setNames] = useState<string[] | null>(null);
  const [error, setError] = useState("");
  const [picked, setPicked] = useState(current);

  useEffect(() => {
    if (!open) return;
    let live = true;
    load().then(
      (list) => {
        if (!live) return;
        setNames(list);
        setPicked((p) => (p && list.includes(p) ? p : (list[0] ?? "")));
      },
      (e: unknown) => live && setError(e instanceof Error ? e.message : "The storages could not be loaded."),
    );
    return () => {
      live = false;
    };
  }, [open, load]);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Turn on {backup}</DialogTitle>
          <DialogDescription>Pick the storage its copies go to.</DialogDescription>
        </DialogHeader>
        {error ? (
          <p role="alert" className="text-sm text-destructive">
            {error}
          </p>
        ) : names === null ? (
          <div className="flex h-16 items-center justify-center">
            <Spinner />
          </div>
        ) : names.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            This server has no storage yet.{" "}
            <Link to="/settings?tab=storages" className="font-medium text-foreground underline underline-offset-4">
              Add a storage in Settings → Storages
            </Link>
            , then turn the backup on.
          </p>
        ) : (
          <div className="grid gap-1.5">
            <Label htmlFor="backup-storage">Storage</Label>
            <select id="backup-storage" className={selectClass} value={picked} onChange={(e) => setPicked(e.target.value)}>
              {names.map((n) => (
                <option key={n} value={n}>
                  {n}
                </option>
              ))}
            </select>
          </div>
        )}
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button disabled={!picked} onClick={() => onPick(picked)}>
            Turn on
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
