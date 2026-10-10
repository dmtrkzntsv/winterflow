import { Badge } from "@/components/ui/badge";
import { AppStatusCode } from "@/context/apps-context-base";
import { cn } from "@/lib/utils";

// Maps a container status code to a label + badge variant. Absence/unknown
// renders muted (we never infer "offline" from missing status).
const STATUS_META: Record<
  number,
  { label: string; variant: "default" | "secondary" | "destructive" | "outline" }
> = {
  [AppStatusCode.Active]: { label: "Running", variant: "default" },
  [AppStatusCode.Idle]: { label: "Idle", variant: "secondary" },
  [AppStatusCode.Restarting]: { label: "Restarting", variant: "secondary" },
  [AppStatusCode.Problematic]: { label: "Problematic", variant: "destructive" },
  [AppStatusCode.Stopped]: { label: "Stopped", variant: "outline" },
  [AppStatusCode.Unknown]: { label: "Unknown", variant: "outline" },
};

export function AppStatusBadge({ status }: { status?: number }) {
  const meta = STATUS_META[status ?? AppStatusCode.Unknown] ?? STATUS_META[0];
  return (
    <Badge variant={meta.variant} className="shrink-0">
      {meta.label}
    </Badge>
  );
}

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

// AppStatusIndicator is the quieter inline form of AppStatusBadge: a colored
// dot plus the label, for headers where a filled pill would shout.
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
