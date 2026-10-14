import { useRef } from "react";
import { MoreHorizontal } from "lucide-react";

import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { cn } from "@/lib/utils";

export type RowMenuItem = {
  label: string;
  // onSelect gets the menu's button: a dialog opened from the menu hands
  // focus back to it when it closes (returnFocusTo).
  onSelect: (trigger: HTMLButtonElement | null) => void;
  destructive?: boolean;
  disabled?: boolean;
  title?: string;
};

// RowMenu is a row's "⋯" menu: its less frequent actions, the destructive
// one last and red. label names the button ("Actions for backup-disk").
// Falsy entries are skipped, so a caller can write `cond && item`.
export function RowMenu({ label, items }: { label: string; items: (RowMenuItem | false | null | undefined)[] }) {
  const trigger = useRef<HTMLButtonElement>(null);
  const shown = items.filter((item): item is RowMenuItem => Boolean(item));
  if (shown.length === 0) return null;
  return (
    <DropdownMenu modal={false}>
      <DropdownMenuTrigger asChild>
        <Button
          ref={trigger}
          size="icon"
          variant="ghost"
          className="size-10 text-muted-foreground sm:size-8"
          aria-label={label}
        >
          <MoreHorizontal className="size-4" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        {shown.map((item) => (
          <DropdownMenuItem
            key={item.label}
            disabled={item.disabled}
            title={item.title}
            className={cn(item.destructive && "text-destructive focus:text-destructive")}
            onSelect={() => item.onSelect(trigger.current)}
          >
            {item.label}
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
