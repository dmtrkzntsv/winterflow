import { useEffect, useRef } from "react";
import { useNavigate } from "react-router-dom";

import { Button } from "@/components/ui/button";
import { ErrorText } from "@/components/error-text";

// CreateBar ends a create form: Cancel and the one filled Create app. On a
// phone it sticks to the bottom of the screen so Create stays in reach;
// from 640px up it sits under the form. invalid adds the one-line summary
// of a refused attempt (each field says what is wrong). waiting holds
// Create back while something the form needs is still being read. error
// is a failed create's message, shown above the bar: it is scrolled into
// view (clear of the sticky bar on a phone) and focused when it appears.
export function CreateBar({
  busy,
  onCreate,
  cancelTo,
  invalid = false,
  waiting = false,
  error = "",
}: {
  busy: boolean;
  onCreate: () => void;
  cancelTo: string;
  invalid?: boolean;
  waiting?: boolean;
  error?: string;
}) {
  const navigate = useNavigate();
  const errorBox = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const el = errorBox.current;
    if (!error || !el) return;
    el.scrollIntoView?.({ block: "nearest" });
    el.focus({ preventScroll: true });
  }, [error]);
  return (
    <>
      {error ? (
        <div ref={errorBox} tabIndex={-1} className="scroll-mb-28 outline-none sm:scroll-mb-4">
          <ErrorText alert icon text={error} />
        </div>
      ) : null}
      <div
        data-testid="create-bar"
        className="sticky bottom-0 z-10 -mx-4 flex flex-wrap items-center justify-end gap-2 border-t bg-background/95 px-4 pt-3 pb-[max(0.75rem,env(safe-area-inset-bottom))] backdrop-blur sm:static sm:mx-0 sm:border-t-0 sm:bg-transparent sm:px-0 sm:pt-2 sm:pb-0 sm:backdrop-blur-none"
      >
        {invalid ? (
          <p role="alert" className="mr-auto text-sm text-destructive">
            Some fields need attention.
          </p>
        ) : null}
        <Button variant="ghost" disabled={busy} onClick={() => navigate(cancelTo)}>
          Cancel
        </Button>
        <Button className="max-sm:flex-1" disabled={busy || waiting} onClick={onCreate}>
          {busy ? "Creating…" : "Create app"}
        </Button>
      </div>
    </>
  );
}
