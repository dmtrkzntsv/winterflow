import { useEffect, useRef } from "react";
import { useBlocker, type Location } from "react-router-dom";

import { ConfirmDialog } from "@/components/confirm-dialog";

// UnsavedChangesGuard asks before a navigation drops unsaved edits: while
// `when`, a navigation that `stays` does not allow (another tab, another
// page — the sidebar, ⌘K, Back) is held, and "Discard unsaved changes?"
// decides: Discard carries it out, Keep editing stays. A reload or closing
// the tab gets the browser's own prompt. Needs a data router.
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
  // proceeding: Discard let the navigation go. The dialog then closes with
  // the blocker of its last render (still "blocked"), and resetting that
  // would block a Back/Forward again (their history.go lands later) and
  // ask once more.
  const proceeding = useRef(false);
  useEffect(() => {
    if (blocker.state === "unblocked") proceeding.current = false;
  }, [blocker.state]);

  useEffect(() => {
    if (!when) return;
    const ask = (e: BeforeUnloadEvent) => {
      e.preventDefault();
      // Older browsers ask only when returnValue is set.
      e.returnValue = "";
    };
    window.addEventListener("beforeunload", ask);
    return () => window.removeEventListener("beforeunload", ask);
  }, [when]);

  return (
    <ConfirmDialog
      open={blocker.state === "blocked"}
      onOpenChange={(open) => {
        if (!open && blocker.state === "blocked" && !proceeding.current) blocker.reset();
      }}
      title="Discard unsaved changes?"
      description={description}
      confirmLabel="Discard changes"
      cancelLabel="Keep editing"
      onConfirm={() => {
        proceeding.current = true;
        blocker.proceed?.();
      }}
    />
  );
}
