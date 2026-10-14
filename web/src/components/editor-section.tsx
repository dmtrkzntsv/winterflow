import type { ReactNode } from "react";

import { cn } from "@/lib/utils";

type Props = {
  title: string;
  description?: ReactNode;
  // action sits under the description on wide screens (e.g. "Add file" or a
  // switch that enables the whole section).
  action?: ReactNode;
  // inlineAction keeps the action beside the title at every width — for a
  // switch that turns the whole section on or off.
  inlineAction?: boolean;
  children?: ReactNode;
  className?: string;
};

// EditorSection is one row of the app editor: a short explanation on the left,
// the fields on the right. Rows are separated by a hairline instead of each
// living in its own card, so a long form reads as one document.
export function EditorSection({
  title,
  description,
  action,
  inlineAction = false,
  children,
  className,
}: Props) {
  return (
    <section
      className={cn(
        "grid gap-4 border-t py-6 first:border-t-0 first:pt-0 md:grid-cols-[14rem_minmax(0,1fr)] md:gap-8",
        className,
      )}
    >
      <div className="space-y-1.5">
        <div
          className={cn(
            "flex items-center justify-between gap-3",
            !inlineAction && "md:block",
          )}
        >
          <h2 className="text-sm font-semibold">{title}</h2>
          {action ? (
            <div className={cn("shrink-0", !inlineAction && "md:hidden")}>
              {action}
            </div>
          ) : null}
        </div>
        {description ? (
          <p className="text-sm text-muted-foreground">{description}</p>
        ) : null}
        {action && !inlineAction ? (
          <div className="hidden pt-2 md:block">{action}</div>
        ) : null}
      </div>
      {children ? <div className="min-w-0 space-y-3">{children}</div> : null}
    </section>
  );
}

// StickyActionBar is an editor's bar of actions (Save, Cancel, …) that
// stays at the top while its long form scrolls; below 1024px it sits under
// the shell's 56px top bar.
export function StickyActionBar({ className, children }: { className?: string; children: ReactNode }) {
  return (
    <div
      className={cn(
        "sticky top-0 z-10 max-lg:top-14 -mx-4 flex flex-wrap items-center gap-2 border-b bg-background/95 px-4 py-3 backdrop-blur supports-[backdrop-filter]:bg-background/80",
        className,
      )}
    >
      {children}
    </div>
  );
}
