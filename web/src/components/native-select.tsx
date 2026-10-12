import type { ComponentProps } from "react";

import { cn } from "@/lib/utils";

// NativeSelect is a plain <select> styled like the design system's inputs
// (the backup forms keep the native control: it is small and a phone shows
// its own picker).
export function NativeSelect({ className, ...props }: ComponentProps<"select">) {
  return (
    <select
      className={cn(
        "h-9 w-full min-w-0 rounded-md border border-input bg-transparent px-3 text-sm shadow-xs outline-none focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 disabled:opacity-50",
        className,
      )}
      {...props}
    />
  );
}
