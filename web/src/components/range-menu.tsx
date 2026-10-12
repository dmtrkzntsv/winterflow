import { ChevronDown, Clock } from "lucide-react";

import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { cn } from "@/lib/utils";
import { METRICS_RANGES, rangeLabel, type MetricsMode, type MetricsRange } from "@/lib/metrics";

// RangePicker is the chart range as one select-like button (clock icon,
// "Last 24 hours", chevron) opening a radio menu of the ranges.
export function RangePicker({
  value,
  onChange,
  className,
}: {
  value: MetricsRange;
  onChange: (r: MetricsRange) => void;
  className?: string;
}) {
  const text = rangeLabel(value);
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          variant="outline"
          size="sm"
          aria-label={`Range: ${text}`}
          className={cn(
            "min-w-0 justify-start gap-2 bg-card font-normal sm:max-w-60 [&>svg]:text-muted-foreground",
            className,
          )}
        >
          <Clock />
          <span className="truncate">{text}</span>
          <ChevronDown className="ml-auto opacity-60" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="min-w-44 origin-(--radix-dropdown-menu-content-transform-origin)">
        <DropdownMenuRadioGroup value={value} onValueChange={(v) => onChange(v as MetricsRange)}>
          {METRICS_RANGES.map((r) => (
            <DropdownMenuRadioItem key={r.value} value={r.value}>
              {r.label}
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

// ModeSwitch is the small Average/Peak segmented switch: one muted track,
// the active half raised on the card colour.
export function ModeSwitch({
  value,
  onChange,
  className,
}: {
  value: MetricsMode;
  onChange: (m: MetricsMode) => void;
  className?: string;
}) {
  const item =
    "h-[26px] min-w-0 rounded-md px-2.5 text-xs font-medium text-muted-foreground hover:bg-transparent hover:text-foreground data-[state=on]:bg-background data-[state=on]:text-foreground data-[state=on]:shadow-sm";
  return (
    <ToggleGroup
      type="single"
      size="sm"
      value={value}
      // Clicking the selected half would unset it; the value stays.
      onValueChange={(v) => v && onChange(v as MetricsMode)}
      aria-label="Average or peak"
      className={cn("h-8 gap-0 rounded-lg bg-muted p-[3px]", className)}
    >
      <ToggleGroupItem value="avg" className={item}>
        Average
      </ToggleGroupItem>
      <ToggleGroupItem value="peak" className={item}>
        Peak
      </ToggleGroupItem>
    </ToggleGroup>
  );
}

// RangeMenu is the charts' header control: the range picker and, when
// onModeChange is given, the Average/Peak switch beside it.
export function RangeMenu({
  range,
  onRangeChange,
  mode,
  onModeChange,
  className,
}: {
  range: MetricsRange;
  onRangeChange: (r: MetricsRange) => void;
  mode?: MetricsMode;
  onModeChange?: (m: MetricsMode) => void;
  className?: string;
}) {
  return (
    <div className={cn("flex flex-wrap items-center gap-2", className)}>
      {onModeChange ? <ModeSwitch value={mode ?? "avg"} onChange={onModeChange} /> : null}
      <RangePicker value={range} onChange={onRangeChange} />
    </div>
  );
}
