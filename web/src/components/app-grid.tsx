import { Link } from "react-router-dom";

import { Card } from "@/components/ui/card";
import { AppIcon } from "@/components/app-icon";
import { AppStatusCode } from "@/context/apps-context-base";
import { useApps } from "@/context/use-apps";
import { cn } from "@/lib/utils";

// Status → a small dot and a word under the tile (twillingate's status
// treatment, not a coloured outline); unknown is just a neutral dot.
const STATUS_STYLE: Record<number, { label: string; dot: string }> = {
  [AppStatusCode.Active]: { label: "Running", dot: "bg-emerald-500" },
  [AppStatusCode.Idle]: { label: "Idle", dot: "bg-muted-foreground/40" },
  [AppStatusCode.Restarting]: { label: "Restarting", dot: "bg-amber-500" },
  [AppStatusCode.Problematic]: { label: "Problematic", dot: "bg-red-500" },
  [AppStatusCode.Stopped]: { label: "Stopped", dot: "bg-muted-foreground/40" },
  [AppStatusCode.Unknown]: { label: "Unknown", dot: "bg-muted-foreground/25" },
};

// AppGrid renders the active server's apps as square tiles: colored icon
// block, name, a status dot with its label, staggered entrance animation. Each tile links to the app's details page.
export function AppGrid() {
  const { apps, statusByApp, loading, error } = useApps();

  if (loading && apps.length === 0) {
    return (
      <div className="flex flex-wrap gap-3">
        <div className="frost h-32 w-32 rounded-xl" />
        <div className="frost h-32 w-32 rounded-xl" />
        <div className="frost h-32 w-32 rounded-xl" />
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
    <div className="flex flex-wrap gap-3">
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
            <Card className="flex h-32 w-32 flex-col items-center p-3.5 transition-colors hover:border-primary/40">
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
                {/* Buttons, not anchors: the tile is already a Link (<a>), and
                    nesting an <a> inside it is invalid interactive content. */}
                {app.domains
                  ?.filter((d) => d.kind === "route")
                  .map((d) => (
                    <button
                      key={d.domain}
                      type="button"
                      onClick={(e) => {
                        e.stopPropagation();
                        e.preventDefault();
                        window.open(
                          `${d.ssl ? "https" : "http"}://${d.domain}`,
                          "_blank",
                          "noopener,noreferrer",
                        );
                      }}
                      className="w-full truncate text-center text-xs text-muted-foreground hover:text-foreground hover:underline"
                    >
                      {d.domain}
                    </button>
                  ))}
              </div>
              <span className="mt-auto flex items-center gap-1.5 text-[11px] text-muted-foreground">
                <span
                  aria-hidden
                  className={cn("size-2 shrink-0 rounded-full", status.dot)}
                />
                {unknown ? null : status.label}
              </span>
            </Card>
          </Link>
        );
      })}
    </div>
  );
}
