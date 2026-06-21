import { Badge } from "@/components/ui/badge";
import { AppStatusCode } from "@/context/apps-context-base";

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
