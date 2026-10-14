import { useCallback, useState } from "react";

// useConfirmTarget holds what a ConfirmDialog is about. The target stays
// after the dialog closes, so the title and text don't go blank while it
// animates out; only `open` says whether it is showing.
export function useConfirmTarget<T>() {
  const [target, setTarget] = useState<T | null>(null);
  const [open, setOpen] = useState(false);
  const ask = useCallback((next: T) => {
    setTarget(next);
    setOpen(true);
  }, []);
  return { target, open, ask, onOpenChange: setOpen };
}
