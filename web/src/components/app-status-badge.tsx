import { AppStatusCode } from "@/context/apps-context-base";
import { cn } from "@/lib/utils";

// Maps a container status code to its label. Absence/unknown renders muted
// (we never infer "offline" from missing status).
const STATUS_META: Record<number, { label: string }> = {
  [AppStatusCode.Active]: { label: "Running" },
  [AppStatusCode.Idle]: { label: "Idle" },
  [AppStatusCode.Restarting]: { label: "Restarting" },
  [AppStatusCode.Problematic]: { label: "Problematic" },
  [AppStatusCode.Stopped]: { label: "Stopped" },
  [AppStatusCode.Unknown]: { label: "Unknown" },
};

const DOT_CLASS: Record<number, string> = {
  [AppStatusCode.Active]: "bg-emerald-500",
  [AppStatusCode.Idle]: "bg-amber-400",
  [AppStatusCode.Restarting]: "bg-amber-400 animate-pulse",
  [AppStatusCode.Problematic]: "bg-red-500",
  [AppStatusCode.Stopped]: "bg-muted-foreground/50",
  [AppStatusCode.Unknown]: "bg-muted-foreground/30",
};

// StatusDot is a colored dot plus a label — the quiet inline status form
// shared by app and volume status.
export function StatusDot({
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
      <span
        className={cn("size-2 shrink-0 rounded-full", dotClassName)}
        aria-hidden
      />
      {label}
    </span>
  );
}

// AppStatusIndicator is an app's status as a colored dot plus the label,
// quiet enough for headers and lists.
export function AppStatusIndicator({ status }: { status?: number }) {
  const code = status ?? AppStatusCode.Unknown;
  const meta = STATUS_META[code] ?? STATUS_META[0];
  return (
    <StatusDot
      label={meta.label}
      dotClassName={DOT_CLASS[code] ?? DOT_CLASS[0]}
      className={cn(
        "shrink-0 text-sm",
        code === AppStatusCode.Problematic
          ? "font-medium text-red-600 dark:text-red-400"
          : "text-foreground",
      )}
    />
  );
}
