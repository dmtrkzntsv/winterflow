import { useEffect, useState, type RefObject } from "react";
import { Link } from "react-router-dom";

import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { InlineError } from "@/components/inline-error";
import { NativeSelect } from "@/components/native-select";
import { Spinner } from "@/components/ui/spinner";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { returnFocusTo } from "@/lib/focus";

// BackupStorageDialog turns a backup on: it picks one of the server's
// storages (load lists their names), or points to Settings → Storages when
// there are none. The parent remounts it per opening.
export function BackupStorageDialog({
  backup,
  current,
  open,
  busy = false,
  error: saveError = "",
  returnFocus,
  load,
  onOpenChange,
  onPick,
}: {
  backup: string;
  current: string;
  open: boolean;
  busy?: boolean; // the backup is being turned on
  error?: string; // why the last attempt to turn it on was refused
  returnFocus?: RefObject<HTMLElement | null>; // where focus goes back on close
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
      <DialogContent className="sm:max-w-md" onCloseAutoFocus={returnFocusTo(returnFocus)}>
        <DialogHeader>
          <DialogTitle>Turn on {backup}</DialogTitle>
          <DialogDescription>Pick the storage its copies go to.</DialogDescription>
        </DialogHeader>
        {error ? (
          <InlineError>{error}</InlineError>
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
            <NativeSelect id="backup-storage" value={picked} onChange={(e) => setPicked(e.target.value)}>
              {names.map((n) => (
                <option key={n} value={n}>
                  {n}
                </option>
              ))}
            </NativeSelect>
          </div>
        )}
        {saveError ? (
          <InlineError className="[overflow-wrap:anywhere]">{saveError}</InlineError>
        ) : null}
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button disabled={!picked || busy} onClick={() => onPick(picked)}>
            {busy ? "Turning on…" : "Turn on"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
