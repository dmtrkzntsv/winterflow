import { useState } from "react";
import { RefreshCw } from "lucide-react";

import { Button } from "@/components/ui/button";
import { formatClock } from "@/lib/kpi";
import { cn } from "@/lib/utils";

// DataFreshness is the quiet line under a page title: "Data as of 05:06 PM"
// and a small refresh button that re-fetches now (twillingate's header).
// Before the first fetch lands it says "Loading data".
export function DataFreshness({
  updatedAt,
  onRefresh,
  className,
}: {
  updatedAt: number | null;
  onRefresh: () => Promise<unknown> | void;
  className?: string;
}) {
  const [busy, setBusy] = useState(false);
  // Busy, the button stays focusable (a disabled one would drop keyboard
  // focus to <body>) and ignores repeat presses.
  const refresh = async () => {
    if (busy) return;
    setBusy(true);
    try {
      await onRefresh();
    } finally {
      setBusy(false);
    }
  };
  return (
    <div className={cn("flex items-center gap-1 text-xs text-muted-foreground tabular-nums", className)}>
      <span data-testid="data-as-of">{updatedAt ? `Data as of ${formatClock(updatedAt)}` : "Loading data"}</span>
      <Button
        type="button"
        variant="ghost"
        size="icon"
        aria-label="Refresh data"
        title="Refresh data"
        aria-busy={busy}
        aria-disabled={busy}
        onClick={() => void refresh()}
        className="size-6 max-sm:size-10 text-muted-foreground hover:text-foreground"
      >
        <RefreshCw className={cn("size-3.5", busy && "animate-spin")} aria-hidden />
      </Button>
    </div>
  );
}
