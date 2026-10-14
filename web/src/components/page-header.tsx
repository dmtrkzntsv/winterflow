import type { ReactNode } from "react";
import { Link } from "react-router-dom";
import { ArrowLeft } from "lucide-react";

import { usePageTitle } from "@/layouts/use-page-title";
import { cn } from "@/lib/utils";

export type PageHeaderProps = {
  title: string;
  subtitle?: ReactNode;
  // back is the page this one sits under ("← Website", "← Templates").
  back?: { to: string; label: string };
  // actions sit top-right on a desktop (one filled primary at most), under
  // the title on a phone.
  actions?: ReactNode;
  className?: string;
};

// PageHeader starts every page: an optional back link, the title (24/600),
// a subtitle and the actions. It also names the page (usePageTitle).
export function PageHeader({ title, subtitle, back, actions, className }: PageHeaderProps) {
  usePageTitle(title);
  return (
    <header className={cn("flex flex-col gap-3 sm:flex-row sm:items-end sm:justify-between", className)}>
      <div className="min-w-0 space-y-1">
        {back ? (
          <Link
            to={back.to}
            className="inline-flex min-h-8 items-center gap-1 text-sm text-muted-foreground max-sm:min-h-10 transition-colors duration-150 hover:text-foreground"
          >
            <ArrowLeft className="size-4" aria-hidden />
            {back.label}
          </Link>
        ) : null}
        <h1 className="truncate text-2xl font-semibold tracking-tight">{title}</h1>
        {subtitle ? <p className="text-sm text-muted-foreground">{subtitle}</p> : null}
      </div>
      {actions ? <div className="flex shrink-0 flex-wrap items-center gap-2">{actions}</div> : null}
    </header>
  );
}
