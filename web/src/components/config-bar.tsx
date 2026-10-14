import { useRef, type ReactNode } from "react";
import { History, MoreHorizontal } from "lucide-react";

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

// ConfigBar is the Configuration tab's bar (the app editor's and a
// template-managed app's). While nothing is edited (dirty false) it is only
// a History button on the right (and the status line, when it says
// something). Once edited it is a sticky bar: from 640px up "Unsaved
// changes", the secondary actions and the one primary; on a phone the
// primary and a ⋯ menu (History, then the secondary actions), in one row,
// so the sticky bar stays one line high, and the status line under it.
// secondary is in menu order; the bar shows them last-first, so the one
// nearest the primary is the first listed.
export function ConfigBar({
  status,
  dirty,
  onOpenHistory,
  secondary = [],
  primary,
}: {
  status?: ReactNode;
  // dirty: there is something to save (or discard).
  dirty: boolean;
  // onOpenHistory opens the app's History (the bar's History button, or the
  // ⋯ menu's first item on a phone); without it there is no History.
  onOpenHistory?: () => void;
  secondary?: BarAction[];
  primary: ReactNode;
}) {
  const phone = useIsPhone();
  // A menu item's action runs once the menu has closed: the menu hands focus
  // back to its ⋯ button as it unmounts, which would take it from the field
  // a refused Save just focused.
  const pending = useRef<(() => void) | null>(null);
  if (!dirty) {
    if (!status && !onOpenHistory) return null;
    return (
      <div className="mb-6 flex min-h-9 flex-wrap items-center justify-end gap-2">
        {status ? <p className="mr-auto text-sm text-muted-foreground">{status}</p> : null}
        {onOpenHistory ? (
          <Button variant="ghost" onClick={onOpenHistory}>
            <History className="size-4" aria-hidden /> History
          </Button>
        ) : null}
      </div>
    );
  }
  if (!phone) {
    return (
      <StickyActionBar className="mb-6">
        <p className="mr-auto text-sm text-muted-foreground">{status}</p>
        {[...secondary].reverse().map((a) => (
          <Button
            key={a.label}
            variant="ghost"
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
  return (
    <>
      <StickyActionBar className={status ? "mb-3 flex-nowrap" : "mb-6 flex-nowrap"}>
        <div className="mr-auto" />
        {primary}
        {onOpenHistory || secondary.length > 0 ? (
          <DropdownMenu modal={false}>
            <DropdownMenuTrigger asChild>
              <Button variant="outline" size="icon" className="shrink-0" aria-label="More save options">
                <MoreHorizontal className="size-4" />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent
              align="end"
              onCloseAutoFocus={() => {
                const run = pending.current;
                pending.current = null;
                run?.();
              }}
            >
              {onOpenHistory ? <DropdownMenuItem onSelect={onOpenHistory}>History</DropdownMenuItem> : null}
              {secondary.map((a) => (
                <DropdownMenuItem
                  key={a.label}
                  disabled={a.disabled}
                  title={a.title}
                  onSelect={() => {
                    pending.current = a.onSelect;
                  }}
                >
                  {a.label}
                </DropdownMenuItem>
              ))}
            </DropdownMenuContent>
          </DropdownMenu>
        ) : null}
      </StickyActionBar>
      {status ? <p className="mb-6 text-sm text-muted-foreground">{status}</p> : null}
    </>
  );
}
