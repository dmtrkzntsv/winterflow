import type { ReactNode } from "react";
import { ChevronRight } from "lucide-react";

import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { cn } from "@/lib/utils";

// Disclosure keeps optional fields out of the way ("More options",
// "Advanced"): one styled toggle, the fields below it once opened.
export function Disclosure({
  label = "More options",
  children,
  defaultOpen = false,
  open,
  onOpenChange,
  className,
}: {
  label?: string;
  children: ReactNode;
  defaultOpen?: boolean;
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
  className?: string;
}) {
  return (
    <Collapsible
      defaultOpen={defaultOpen}
      open={open}
      onOpenChange={onOpenChange}
      className={cn("space-y-3", className)}
    >
      <CollapsibleTrigger className="group inline-flex min-h-10 items-center gap-1.5 rounded-md text-sm font-medium text-muted-foreground transition-colors duration-150 hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
        <ChevronRight
          className="size-4 transition-transform duration-150 group-data-[state=open]:rotate-90 motion-reduce:transition-none"
          aria-hidden
        />
        {label}
      </CollapsibleTrigger>
      <CollapsibleContent className="space-y-4">{children}</CollapsibleContent>
    </Collapsible>
  );
}
