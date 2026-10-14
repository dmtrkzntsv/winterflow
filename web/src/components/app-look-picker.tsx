import { useState } from "react";

import { Button } from "@/components/ui/button";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { AppIcon } from "@/components/app-icon";
import { IconGrid } from "@/components/icon-picker";
import { useIsPhone } from "@/hooks/use-is-phone";
import { APP_COLORS, DEFAULT_APP_COLOR, colorName, type AppLook } from "@/lib/app-look";
import { cn } from "@/lib/utils";

// AppLookPicker is the app's tile as a button: it opens the icon grid and
// the colours. onReset hands the choice back to the name. The create forms
// and Settings → General share it.
export function AppLookPicker({
  name,
  icon,
  color,
  onPick,
  onReset,
  disabled = false,
}: {
  name: string;
  icon: string;
  color: string;
  onPick: (look: AppLook) => void;
  onReset: () => void;
  disabled?: boolean;
}) {
  const [open, setOpen] = useState(false);
  const phone = useIsPhone();
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button
          type="button"
          variant="outline"
          className="size-10 shrink-0 p-0"
          aria-label="Change icon and colour"
          title="Change icon and colour"
          disabled={disabled}
        >
          <AppIcon name={name} icon={icon} color={color} className="size-7" />
        </Button>
      </PopoverTrigger>
      <PopoverContent
        className="w-72 space-y-3 p-3"
        align="start"
        // Radix focuses the first field (the icon search) on open; on a phone
        // that raises the keyboard over the grid.
        onOpenAutoFocus={phone ? (e) => e.preventDefault() : undefined}
      >
        <IconGrid value={icon} onPick={(next) => onPick({ icon: next, color })} />
        <div className="space-y-1.5">
          <p className="text-xs font-medium text-muted-foreground">Colour</p>
          <div className="flex flex-wrap items-center gap-1.5">
            {APP_COLORS.map((c) => (
              <button
                key={c}
                type="button"
                aria-label={colorName(c)}
                title={colorName(c)}
                aria-pressed={color === c}
                className={cn(
                  "size-7 rounded-md max-sm:size-10 ring-offset-2 ring-offset-background",
                  color === c && "ring-2 ring-primary",
                )}
                style={{ backgroundColor: c }}
                onClick={() => onPick({ icon, color: c })}
              />
            ))}
            <input
              type="color"
              aria-label="Custom colour"
              value={color || DEFAULT_APP_COLOR}
              className="h-7 w-10 cursor-pointer max-sm:h-10 rounded-md border bg-transparent p-0.5"
              onChange={(e) => onPick({ icon, color: e.target.value })}
            />
          </div>
        </div>
        <Button
          type="button"
          variant="link"
          size="sm"
          className="h-auto p-0"
          onClick={() => {
            onReset();
            setOpen(false);
          }}
        >
          Pick automatically from the name
        </Button>
      </PopoverContent>
    </Popover>
  );
}
