import type { ReactNode } from "react";

import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { InlineError } from "@/components/inline-error";
import { cn } from "@/lib/utils";

// PageSpinner holds a page's (or a tab's) place while its first read runs.
export function PageSpinner() {
  return (
    <div className="flex min-h-60 items-center justify-center">
      <Spinner />
    </div>
  );
}

// LoadError replaces a page (or a tab) whose read failed: why, and Retry.
export function LoadError({ message, onRetry, className }: { message: string; onRetry: () => void; className?: string }) {
  return (
    <div className={cn("flex min-h-60 flex-col items-center justify-center gap-3", className)}>
      <InlineError>{message}</InlineError>
      <Button variant="outline" size="sm" onClick={onRetry}>
        Retry
      </Button>
    </div>
  );
}

// EmptyNote stands in for a list with nothing in it yet.
export function EmptyNote({ className, children }: { className?: string; children: ReactNode }) {
  return <p className={cn("rounded-md border border-dashed p-4 text-sm text-muted-foreground", className)}>{children}</p>;
}
