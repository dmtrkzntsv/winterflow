import type { ReactNode } from "react";
import { CircleX } from "lucide-react";

import { cn } from "@/lib/utils";

// ErrorIcon is the cross an error leads with (in a flex row).
export function ErrorIcon() {
  return <CircleX className="mt-0.5 size-4 shrink-0" />;
}

// InlineError is a failure kept in view next to the control that caused it
// (announced to screen readers). With icon it leads with a cross.
export function InlineError({ icon, className, children }: { icon?: boolean; className?: string; children: ReactNode }) {
  return (
    <p role="alert" className={cn("text-sm text-destructive", icon && "flex items-start gap-1.5", className)}>
      {icon ? (
        <>
          <ErrorIcon />{" "}
        </>
      ) : null}
      {children}
    </p>
  );
}
