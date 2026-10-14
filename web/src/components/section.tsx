import { useId, type ReactNode } from "react";
import { Link } from "react-router-dom";

import { cn } from "@/lib/utils";

// Section is a titled part of a page (15/600 title, optional description
// and actions) — a region named by its title.
export function Section({
  title,
  description,
  actions,
  children,
  className,
}: {
  title: string;
  description?: ReactNode;
  actions?: ReactNode;
  children: ReactNode;
  className?: string;
}) {
  const id = useId();
  return (
    <section aria-labelledby={id} className={cn("space-y-3", className)}>
      <div className="flex flex-wrap items-end justify-between gap-2">
        <div className="min-w-0 space-y-0.5">
          <h2 id={id} className="text-[15px] font-semibold leading-6">
            {title}
          </h2>
          {description ? <p className="text-sm text-muted-foreground">{description}</p> : null}
        </div>
        {actions ? <div className="flex flex-wrap items-center gap-2">{actions}</div> : null}
      </div>
      {children}
    </section>
  );
}

// RowList is a flat list of rows in one soft frame; on a phone the frame
// goes and every row is its own card.
export function RowList({ children, label, className }: { children: ReactNode; label?: string; className?: string }) {
  return (
    <ul
      aria-label={label}
      className={cn(
        "divide-y divide-border/70 overflow-hidden rounded-xl border bg-card",
        "max-sm:space-y-2 max-sm:divide-y-0 max-sm:overflow-visible max-sm:rounded-none max-sm:border-0 max-sm:bg-transparent",
        className,
      )}
    >
      {children}
    </ul>
  );
}

// Row is one item of a RowList: leading visual, title (a link covering the
// row when `to` is set), description, detail (under it, wrapping, above the
// link: an error with its Details), meta and actions (above the link).
export function Row({
  title,
  leading,
  description,
  detail,
  meta,
  actions,
  to,
  className,
  testId,
}: {
  title: ReactNode;
  leading?: ReactNode;
  description?: ReactNode;
  detail?: ReactNode;
  meta?: ReactNode;
  actions?: ReactNode;
  to?: string;
  className?: string;
  testId?: string;
}) {
  return (
    <li
      data-testid={testId}
      className={cn(
        "relative flex flex-col gap-2 px-4 py-3 transition-colors duration-150 sm:flex-row sm:items-center sm:gap-4",
        "max-sm:rounded-xl max-sm:border max-sm:bg-card",
        to && "hover:bg-muted/40",
        className,
      )}
    >
      <div className="flex min-w-0 flex-1 items-center gap-3">
        {leading ? <div className="shrink-0">{leading}</div> : null}
        <div className="min-w-0">
          <div className="truncate text-sm font-medium">
            {to ? (
              <Link to={to} className="after:absolute after:inset-0 max-sm:after:rounded-xl focus-visible:underline focus-visible:outline-none focus-visible:after:ring-2 focus-visible:after:ring-inset focus-visible:after:ring-ring">
                {title}
              </Link>
            ) : (
              title
            )}
          </div>
          {description ? <div className="truncate text-xs text-muted-foreground">{description}</div> : null}
          {detail ? <div className="relative z-10 mt-1">{detail}</div> : null}
        </div>
      </div>
      {meta ? <div className="text-xs text-muted-foreground sm:text-right">{meta}</div> : null}
      {actions ? <div className="relative z-10 flex items-center gap-1">{actions}</div> : null}
    </li>
  );
}
