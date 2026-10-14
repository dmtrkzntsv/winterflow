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

// ConfigBar is the Configuration tab's sticky bar (the app editor's and a
// template-managed app's): a status line, History, the secondary actions
// and the one primary. From 640px up they all sit in the bar; on a phone it
// keeps the primary and a ⋯ menu (History, then the secondary actions), in
// one row, so the sticky bar stays one line high, and the status line sits
// under it. secondary is in menu order; the bar shows them last-first, so
// the one nearest the primary is the first listed.
export function ConfigBar({
  status,
  onOpenHistory,
  secondary = [],
  primary,
}: {
  status?: ReactNode;
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
  if (!phone) {
    return (
      <StickyActionBar className="mb-6">
        <p className="mr-auto text-sm text-muted-foreground">{status}</p>
        {onOpenHistory ? (
          <Button variant="outline" onClick={onOpenHistory}>
            <History className="size-4" aria-hidden /> History
          </Button>
        ) : null}
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
