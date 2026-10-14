import { useState } from "react";

import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { ScrollArea } from "@/components/ui/scroll-area";
import { cn } from "@/lib/utils";
import { APP_ICON_NAMES, getAppIcon } from "@/lib/app-icons";
import { AppIcon } from "@/components/app-icon";

// IconGrid is the searchable grid of app icons.
export function IconGrid({ value, onPick }: { value?: string; onPick: (name: string) => void }) {
  const [query, setQuery] = useState("");

  const filtered = query
    ? APP_ICON_NAMES.filter((n) =>
        n.toLowerCase().includes(query.toLowerCase()),
      )
    : APP_ICON_NAMES;

  return (
    <div>
      <Input
        autoFocus
        placeholder="Search icons…"
        aria-label="Search icons"
        value={query}
        onChange={(e) => setQuery(e.target.value)}
        className="mb-2"
      />
      <ScrollArea className="h-56">
        <div className="grid grid-cols-6 gap-1 pr-2">
          {filtered.map((name) => {
            const Icon = getAppIcon(name)!;
            return (
              <button
                key={name}
                type="button"
                title={name}
                aria-label={name}
                aria-pressed={value === name}
                className={cn(
                  "flex aspect-square items-center justify-center rounded-md hover:bg-muted",
                  value === name && "bg-muted ring-2 ring-primary",
                )}
                onClick={() => onPick(name)}
              >
                <Icon className="size-5" />
              </button>
            );
          })}
          {filtered.length === 0 ? (
            <p className="col-span-6 py-4 text-center text-sm text-muted-foreground">
              No icons
            </p>
          ) : null}
        </div>
      </ScrollArea>
    </div>
  );
}

export function IconPicker({
  value,
  color,
  name,
  onChange,
  label = "Icon",
  disabled = false,
}: {
  value?: string;
  // name gives the tile its fallback initial (as in the header).
  name?: string;
  color?: string;
  onChange: (icon: string) => void;
  // label names the trigger button for assistive technology.
  label?: string;
  disabled?: boolean;
}) {
  const [open, setOpen] = useState(false);

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button variant="outline" className="h-12 w-12 p-0" aria-label={label} disabled={disabled}>
          <AppIcon icon={value} name={name} color={color} className="size-8" />
        </Button>
      </PopoverTrigger>
      <PopoverContent className="w-72 p-2" align="start">
        <IconGrid
          value={value}
          onPick={(next) => {
            onChange(next);
            setOpen(false);
          }}
        />
      </PopoverContent>
    </Popover>
  );
}
