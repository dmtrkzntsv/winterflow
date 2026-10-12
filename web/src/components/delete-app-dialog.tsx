import { useState } from "react";
import { Trash2 } from "lucide-react";

import { Button } from "@/components/ui/button";
import { buttonVariants } from "@/components/ui/button-variants";
import { Checkbox } from "@/components/ui/checkbox";
import { Label } from "@/components/ui/label";
import { Spinner } from "@/components/ui/spinner";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import type { DeleteChoice } from "@/lib/delete-app";
import { cn } from "@/lib/utils";

// DeleteAppDialog confirms deleting the app and, opt-in, its volumes and its
// backups.
export function DeleteAppDialog({
  name,
  disabled,
  deleting = false,
  onConfirm,
}: {
  name: string;
  disabled: boolean;
  deleting?: boolean; // the delete is under way: it can take minutes
  onConfirm: (choice: DeleteChoice) => void;
}) {
  return (
    <div className="grid justify-items-start gap-2">
      <AlertDialog>
        <AlertDialogTrigger asChild>
          <Button
            variant="outline"
            className="border-destructive/40 text-destructive hover:bg-destructive/10 hover:text-destructive"
            disabled={disabled || deleting}
          >
            {deleting ? <Spinner aria-hidden className="size-4" /> : <Trash2 className="size-4" />}{" "}
            {deleting ? `Deleting ${name}…` : `Delete ${name}`}
          </Button>
        </AlertDialogTrigger>
        <AlertDialogContent>
          <DeleteAppConfirm name={name} onConfirm={onConfirm} />
        </AlertDialogContent>
      </AlertDialog>
      {deleting ? (
        <p role="status" className="text-sm text-muted-foreground">
          Deleting the app. This can take a few minutes, longer when its backups are deleted from a storage. Keep this page open.
        </p>
      ) : null}
    </div>
  );
}

// DeleteAppConfirm owns the choices. AlertDialogContent unmounts it on
// close, so every opening starts with the safe default (unticked).
function DeleteAppConfirm({ name, onConfirm }: { name: string; onConfirm: (choice: DeleteChoice) => void }) {
  const [withVolumes, setWithVolumes] = useState(false);
  const [withBackups, setWithBackups] = useState(false);
  const label = withVolumes && withBackups
    ? "Delete app, volumes and backups"
    : withVolumes
      ? "Delete app and volumes"
      : withBackups
        ? "Delete app and backups"
        : "Delete app";
  return (
    <>
      <AlertDialogHeader>
        <AlertDialogTitle>Delete {name}?</AlertDialogTitle>
        <AlertDialogDescription>
          Its containers are stopped and removed, along with the deployment and every stored revision. This cannot be
          undone.
        </AlertDialogDescription>
      </AlertDialogHeader>
      <Choice
        id="delete-volumes"
        checked={withVolumes}
        onChange={setWithVolumes}
        label="Also delete its volumes"
        hint="Permanently erases the data in this app's Docker volumes. Unticked, the volumes are kept; you can delete them later in Server Settings → Volumes."
      />
      <Choice
        id="delete-backups"
        checked={withBackups}
        onChange={setWithBackups}
        label="Also delete its backups"
        hint="Permanently deletes every copy of this app in its backup storages. Unticked, the copies stay on the storage and expire per the server's retention settings; you can delete them sooner in Server Settings → Storages."
      />
      <AlertDialogFooter>
        <AlertDialogCancel>Cancel</AlertDialogCancel>
        <AlertDialogAction
          className={buttonVariants({ variant: "destructive" })}
          onClick={() => onConfirm({ withVolumes, withBackups })}
        >
          {label}
        </AlertDialogAction>
      </AlertDialogFooter>
    </>
  );
}

// Choice is one opt-in: neutral until ticked, then unmistakably
// destructive — the safe choice (keep the data) is the default.
function Choice({
  id,
  checked,
  onChange,
  label,
  hint,
}: {
  id: string;
  checked: boolean;
  onChange: (v: boolean) => void;
  label: string;
  hint: string;
}) {
  return (
    <div
      className={cn(
        "flex items-start gap-3 rounded-md border p-3 transition-colors",
        checked && "border-destructive/50 bg-destructive/5",
      )}
    >
      <Checkbox
        id={id}
        className="mt-px data-[state=checked]:border-destructive data-[state=checked]:bg-destructive data-[state=checked]:text-destructive-foreground"
        aria-describedby={`${id}-hint`}
        checked={checked}
        onCheckedChange={(v) => onChange(v === true)}
      />
      <div className="grid gap-1">
        <Label htmlFor={id} className={cn("cursor-pointer leading-snug", checked && "text-destructive")}>
          {label}
        </Label>
        <p id={`${id}-hint`} className="text-sm leading-snug text-muted-foreground">
          {hint}
        </p>
      </div>
    </div>
  );
}
