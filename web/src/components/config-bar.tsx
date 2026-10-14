import type { ReactNode } from "react";
import { MoreHorizontal } from "lucide-react";

import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { StickyActionBar } from "@/components/editor-section";
import { useIsPhone } from "@/hooks/use-is-phone";

export type BarAction = {
  label: string;
  onSelect: () => void;
  disabled?: boolean;
  title?: string;
};

// ConfigBar is the Configuration tab's sticky bar (the app editor's and a
// template-managed app's): a status line, the page's actions, the
// secondary actions and the one primary. From 640px up they all sit in the
// bar; on a phone it keeps the primary and a ⋯ menu (History, then the
// secondary actions), in one row, so the sticky bar stays one line high.
// secondary is in menu order; the bar shows them last-first, so the one
// nearest the primary is the first listed.
export function ConfigBar({
  status,
  headerActions,
  onOpenHistory,
  secondary = [],
  primary,
}: {
  status?: ReactNode;
  // headerActions are the page's buttons (History); onOpenHistory is how
  // the ⋯ menu opens it on a phone (without it the buttons stay in the bar).
  headerActions?: ReactNode;
  onOpenHistory?: () => void;
  secondary?: BarAction[];
  primary: ReactNode;
}) {
  const phone = useIsPhone();
  if (!phone) {
    return (
      <StickyActionBar className="mb-6">
        <p className="mr-auto text-sm text-muted-foreground">{status}</p>
        {headerActions}
        {[...secondary].reverse().map((a, i) => (
          <Button
            key={a.label}
            variant={i === 0 ? "ghost" : "outline"}
            disabled={a.disabled}
            title={a.title}
            onClick={a.onSelect}
          >
            {a.label}
          </Button>
        ))}
        {primary}
      </StickyActionBar>
    );
  }
  const inMenu = onOpenHistory !== undefined;
  return (
    <StickyActionBar className="mb-6 flex-nowrap">
      <p className="mr-auto min-w-0 flex-1 text-sm text-muted-foreground">{status}</p>
      {inMenu ? null : headerActions}
      {primary}
      {inMenu || secondary.length > 0 ? (
        <DropdownMenu modal={false}>
          <DropdownMenuTrigger asChild>
            <Button variant="outline" size="icon" className="shrink-0" aria-label="More save options">
              <MoreHorizontal className="size-4" />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            {inMenu ? <DropdownMenuItem onSelect={onOpenHistory}>History</DropdownMenuItem> : null}
            {secondary.map((a) => (
              <DropdownMenuItem key={a.label} disabled={a.disabled} title={a.title} onSelect={a.onSelect}>
                {a.label}
              </DropdownMenuItem>
            ))}
          </DropdownMenuContent>
        </DropdownMenu>
      ) : null}
    </StickyActionBar>
  );
}
