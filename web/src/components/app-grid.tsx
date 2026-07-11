import { Link } from "react-router-dom";

import { Card } from "@/components/ui/card";
import { AppIcon } from "@/components/app-icon";
import { AppStatusCode } from "@/context/apps-context-base";
import { useApps } from "@/context/use-apps";
import { cn } from "@/lib/utils";

// Status → card accents, mirroring v1's AppCard: the border and label carry
// the container status color; unknown stays neutral.
const STATUS_STYLE: Record<
  number,
  { label: string; border: string; text: string; dot: string }
> = {
  [AppStatusCode.Active]: {
    label: "Running",
    border: "border-green-500",
    text: "text-green-600",
    dot: "bg-green-500",
  },
  [AppStatusCode.Idle]: {
    label: "Idle",
    border: "border-gray-400",
    text: "text-gray-500",
    dot: "bg-gray-400",
  },
  [AppStatusCode.Restarting]: {
    label: "Restarting",
    border: "border-yellow-500",
    text: "text-yellow-600",
    dot: "bg-yellow-500",
  },
  [AppStatusCode.Problematic]: {
    label: "Problematic",
    border: "border-red-500",
    text: "text-red-600",
    dot: "bg-red-500",
  },
  [AppStatusCode.Stopped]: {
    label: "Stopped",
    border: "border-gray-400",
    text: "text-gray-500",
    dot: "bg-gray-400",
  },
  [AppStatusCode.Unknown]: {
    label: "Unknown",
    border: "border-gray-300",
    text: "text-gray-400",
    dot: "bg-gray-300",
  },
};

// AppGrid renders the active server's apps as v1-style square tiles: colored
// icon block, status-colored border, status label, hover tilt, staggered
// entrance animation. Each tile links to the app's details page.
export function AppGrid() {
  const { apps, statusByApp, loading, error } = useApps();

  if (loading && apps.length === 0) {
    return (
      <div className="flex flex-wrap gap-4">
        <div className="bg-muted/50 h-32 w-32 rounded-xl" />
        <div className="bg-muted/50 h-32 w-32 rounded-xl" />
        <div className="bg-muted/50 h-32 w-32 rounded-xl" />
      </div>
    );
  }

  if (error) {
    return <p className="text-sm text-destructive">{error}</p>;
  }

  if (apps.length === 0) {
    return (
      <div className="flex min-h-40 flex-col items-center justify-center rounded-xl border border-dashed text-center">
        <p className="font-medium">No apps yet</p>
        <p className="mt-1 text-sm text-muted-foreground">
          Deploy an application to see it here.
        </p>
      </div>
    );
  }

  return (
    <div className="flex flex-wrap gap-4">
      {apps.map((app, index) => {
        const status =
          STATUS_STYLE[statusByApp[app.id] ?? AppStatusCode.Unknown] ??
          STATUS_STYLE[AppStatusCode.Unknown];
        const unknown =
          (statusByApp[app.id] ?? AppStatusCode.Unknown) ===
          AppStatusCode.Unknown;
        return (
          <Link
            key={app.id}
            to={`/app/${app.id}`}
            title={`${app.name}: ${status.label}`}
            className="app-card-in block"
            style={{ animationDelay: `${index * 40}ms` }}
          >
            <Card
              className={cn(
                "flex h-32 w-32 flex-col items-center border-2 p-4 transition-all hover:rotate-3 hover:bg-muted/50",
                status.border,
              )}
            >
              <div className="flex flex-1 flex-col items-center gap-2">
                <AppIcon
                  name={app.name}
                  icon={app.icon}
                  color={app.color}
                  className="size-12 rounded-lg"
                />
                <span className="w-full truncate text-center text-sm font-medium">
                  {app.name}
                </span>
                {app.domains
                  ?.filter((d) => d.kind === "route")
                  .map((d) => (
                    <a
                      key={d.domain}
                      href={`${d.ssl ? "https" : "http"}://${d.domain}`}
                      target="_blank"
                      rel="noreferrer"
                      onClick={(e) => e.stopPropagation()}
                      className="w-full truncate text-center text-xs text-muted-foreground hover:text-foreground hover:underline"
                    >
                      {d.domain}
                    </a>
                  ))}
              </div>
              {unknown ? (
                <div className="mt-auto flex items-center justify-center">
                  <div className={cn("size-2 rounded-full", status.dot)} />
                </div>
              ) : (
                <span
                  className={cn("mt-auto text-[10px] font-medium", status.text)}
                >
                  {status.label}
                </span>
              )}
            </Card>
          </Link>
        );
      })}
    </div>
  );
}
