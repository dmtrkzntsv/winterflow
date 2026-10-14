import { useBlocker, type Location } from "react-router-dom";

import { ConfirmDialog } from "@/components/confirm-dialog";

// UnsavedChangesGuard asks before a navigation drops unsaved edits: while
// `when`, a navigation that `stays` does not allow (another tab, another
// page — the sidebar, ⌘K) is held, and "Discard unsaved changes?" decides:
// Discard carries it out, Keep editing stays. Needs a data router.
export function UnsavedChangesGuard({
  when,
  stays,
  description,
}: {
  when: boolean;
  stays: (next: Location, current: Location) => boolean;
  description: string;
}) {
  const blocker = useBlocker(({ currentLocation, nextLocation }) => when && !stays(nextLocation, currentLocation));
  return (
    <ConfirmDialog
      open={blocker.state === "blocked"}
      onOpenChange={(open) => {
        if (!open && blocker.state === "blocked") blocker.reset();
      }}
      title="Discard unsaved changes?"
      description={description}
      confirmLabel="Discard changes"
      cancelLabel="Keep editing"
      onConfirm={() => blocker.proceed?.()}
    />
  );
}
