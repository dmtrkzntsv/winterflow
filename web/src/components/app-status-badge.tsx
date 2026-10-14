import { AppStatusCode } from "@/context/apps-context-base";
import { appStatus, TONE_DOT_CLASS } from "@/lib/status";
import { cn } from "@/lib/utils";

// DotLabel is a coloured dot plus a label — the quiet inline status form
// shared by app and volume status.
export function DotLabel({
  label,
  dotClassName,
  className,
}: {
  label: string;
  dotClassName: string;
  className?: string;
}) {
  return (
    <span className={cn("inline-flex items-center gap-1.5", className)}>
      <span className={cn("size-2 shrink-0 rounded-full", dotClassName)} aria-hidden />
      {label}
    </span>
  );
}

// AppStatusIndicator is an app's status as a coloured dot plus the label,
// quiet enough for headers and lists.
export function AppStatusIndicator({ status }: { status?: number }) {
  const info = appStatus(status);
  return (
    <DotLabel
      label={info.label}
      dotClassName={cn(TONE_DOT_CLASS[info.tone], info.pulse && "animate-pulse motion-reduce:animate-none")}
      className={cn(
        "shrink-0 text-sm",
        status === AppStatusCode.Problematic ? "font-medium text-status-bad" : "text-foreground",
      )}
    />
  );
}
