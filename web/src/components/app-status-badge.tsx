import { StatusDot } from "@/components/status";
import { AppStatusCode } from "@/context/apps-context-base";
import { appStatus } from "@/lib/status";
import { cn } from "@/lib/utils";

// AppStatusIndicator is an app's status as a coloured dot plus the label,
// quiet enough for headers and lists.
export function AppStatusIndicator({ status }: { status?: number }) {
  const info = appStatus(status);
  return (
    <span
      className={cn(
        "inline-flex shrink-0 items-center gap-1.5 text-sm",
        status === AppStatusCode.Problematic ? "font-medium text-status-bad" : "text-foreground",
      )}
    >
      <StatusDot tone={info.tone} pulse={info.pulse} />
      {info.label}
    </span>
  );
}
