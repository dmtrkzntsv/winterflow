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
import type { SharedVariable } from "@/lib/shared-variables";

// DeleteVariableDialog confirms deleting a shared variable; `variable` null
// = closed. The caller words what deleting means in its scope.
export function DeleteVariableDialog({
  variable,
  description,
  onCancel,
  onConfirm,
}: {
  variable: SharedVariable | null;
  description: string;
  onCancel: () => void;
  onConfirm: (v: SharedVariable) => void;
}) {
  return (
    <AlertDialog
      open={variable !== null}
      onOpenChange={(o) => {
        if (!o) onCancel();
      }}
    >
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Delete {variable?.name}?</AlertDialogTitle>
          <AlertDialogDescription>{description}</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction
            onClick={() => {
              const v = variable;
              onCancel();
              if (v) onConfirm(v);
            }}
          >
            Delete
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
