import { useState } from "react";

import { Checkbox } from "@/components/ui/checkbox";
import { Label } from "@/components/ui/label";
import { ConfirmDialog } from "@/components/confirm-dialog";
import type { DeleteChoice } from "@/lib/delete-app";
import { cn } from "@/lib/utils";

// DeleteAppDialog confirms deleting an app and, opt-in, its volumes and its
// backups. Every opening starts with the safe default (unticked). onConfirm
// runs the delete: the dialog shows it pending (it can take minutes), closes
// when it resolves and stays open when it rejects.
export function DeleteAppDialog({
  name,
  open,
  onOpenChange,
  onConfirm,
}: {
  name: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onConfirm: (choice: DeleteChoice) => Promise<void>;
}) {
  const [withVolumes, setWithVolumes] = useState(false);
  const [withBackups, setWithBackups] = useState(false);
  const [deleting, setDeleting] = useState(false);
  // A new opening resets the choices (adjusted during render).
  const [wasOpen, setWasOpen] = useState(open);
  if (wasOpen !== open) {
    setWasOpen(open);
    if (open) {
      setWithVolumes(false);
      setWithBackups(false);
    }
  }
  const label =
    withVolumes && withBackups
      ? "Delete app, volumes and backups"
      : withVolumes
        ? "Delete app and volumes"
        : withBackups
          ? "Delete app and backups"
          : "Delete app";
  return (
    <ConfirmDialog
      open={open}
      onOpenChange={onOpenChange}
      title={`Delete ${name}?`}
      description="Its containers are stopped and removed, along with the deployment and its whole history. This cannot be undone."
      confirmLabel={label}
      pendingLabel={`Deleting ${name}…`}
      onConfirm={async () => {
        setDeleting(true);
        try {
          await onConfirm({ withVolumes, withBackups });
        } finally {
          setDeleting(false);
        }
      }}
    >
      <Choice
        id="delete-volumes"
        checked={withVolumes}
        disabled={deleting}
        onChange={setWithVolumes}
        label="Also delete its volumes"
        hint="Permanently erases the data in this app's Docker volumes. Unticked, the volumes are kept; you can delete them later in Settings → Docker."
      />
      <Choice
        id="delete-backups"
        checked={withBackups}
        disabled={deleting}
        onChange={setWithBackups}
        label="Also delete its backups"
        hint="Permanently deletes every copy of this app in its backup storages. Unticked, the copies stay in the storage and expire per the server's retention settings; you can delete them sooner in Settings → Storages."
      />
      {deleting ? (
        <p role="status" className="text-sm text-muted-foreground">
          Deleting the app. This can take a few minutes, longer when its backups are deleted from a storage. Keep this page open.
        </p>
      ) : null}
    </ConfirmDialog>
  );
}

// Choice is one opt-in: neutral until ticked, then unmistakably
// destructive — the safe choice (keep the data) is the default.
function Choice({
  id,
  checked,
  disabled,
  onChange,
  label,
  hint,
}: {
  id: string;
  checked: boolean;
  disabled: boolean;
  onChange: (v: boolean) => void;
  label: string;
  hint: string;
}) {
  return (
    <div
      className={cn(
        "flex items-start gap-3 rounded-md border p-3 transition-colors duration-150",
        checked && "border-destructive/50 bg-destructive/5",
      )}
    >
      <Checkbox
        id={id}
        className="mt-px data-[state=checked]:border-destructive data-[state=checked]:bg-destructive data-[state=checked]:text-destructive-foreground"
        aria-describedby={`${id}-hint`}
        checked={checked}
        disabled={disabled}
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
