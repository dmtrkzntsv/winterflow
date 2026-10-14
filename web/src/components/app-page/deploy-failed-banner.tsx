import { useId, useState } from "react";

import { Button } from "@/components/ui/button";
import { ErrorIcon } from "@/components/inline-error";
import { errorHeadline } from "@/lib/errors";

// DeployFailedBanner says an app couldn't start: the line of the error that
// says why, the full output behind Show details (wrapping, in its own
// scroll box), and the way to fix it (onOpenConfiguration, when given).
export function DeployFailedBanner({ error, onOpenConfiguration }: { error: string; onOpenConfiguration?: () => void }) {
  const [open, setOpen] = useState(false);
  const detailsId = useId();
  const line = errorHeadline(error);
  const more = line !== error.trim();
  return (
    <div role="alert" className="min-w-0 space-y-2 rounded-xl border border-destructive/40 bg-destructive/5 p-4 text-sm">
      <p className="flex items-start gap-1.5 text-destructive">
        <ErrorIcon />
        <span className="max-h-40 min-w-0 overflow-y-auto break-words whitespace-pre-wrap [overflow-wrap:anywhere]">
          <span className="font-medium">Couldn't start: </span>
          {line || "the deploy failed."}
        </span>
      </p>
      <div className="flex flex-wrap gap-2">
        {onOpenConfiguration ? (
          <Button size="sm" variant="outline" onClick={onOpenConfiguration}>
            Open Configuration
          </Button>
        ) : null}
        {more ? (
          <Button size="sm" variant="ghost" aria-expanded={open} aria-controls={detailsId} onClick={() => setOpen((o) => !o)}>
            {open ? "Hide details" : "Show details"}
          </Button>
        ) : null}
      </div>
      {more && open ? (
        <pre
          id={detailsId}
          className="max-h-72 overflow-auto rounded-md border bg-muted/40 p-2 font-mono text-xs break-words whitespace-pre-wrap [overflow-wrap:anywhere]"
        >
          {error}
        </pre>
      ) : null}
    </div>
  );
}
