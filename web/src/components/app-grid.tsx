import { Link } from "react-router-dom";
import { Boxes } from "lucide-react";

import { Card } from "@/components/ui/card";
import { AppIcon } from "@/components/app-icon";
import { AppStatusCode } from "@/context/apps-context-base";
import { useApps } from "@/context/use-apps";
import { appLabel } from "@/lib/kpi";
import { TILE_GRID } from "@/lib/grid";
import { cn } from "@/lib/utils";

// Status → a small dot and a word (twillingate's status treatment, not a
// coloured outline); unknown is just a neutral dot.
const STATUS_STYLE: Record<number, { label: string; dot: string }> = {
  [AppStatusCode.Active]: { label: "Running", dot: "bg-emerald-500" },
  [AppStatusCode.Idle]: { label: "Idle", dot: "bg-muted-foreground/40" },
  [AppStatusCode.Restarting]: { label: "Restarting", dot: "bg-amber-500" },
  [AppStatusCode.Problematic]: { label: "Problematic", dot: "bg-red-500" },
  [AppStatusCode.Stopped]: { label: "Stopped", dot: "bg-muted-foreground/40" },
  [AppStatusCode.Unknown]: { label: "Unknown", dot: "bg-muted-foreground/25" },
};

function AppTileSkeleton() {
  return (
    <Card role="status" aria-label="Loading apps" className="flex items-center gap-3 p-3.5">
      <div className="frost size-10 shrink-0 rounded-lg" />
      <div className="flex-1 space-y-2">
        <div className="frost h-3.5 w-24 rounded" />
        <div className="frost h-3 w-32 rounded" />
      </div>
    </Card>
  );
}

// AppGrid renders the active server's apps as cards in the server cards'
// language: icon, name, a status dot with its word, and the public route
// (or, for an app without a name yet, its id). Each card links to the app.
export function AppGrid() {
  const { apps, statusByApp, loading, error } = useApps();

  if (loading && apps.length === 0) {
    return (
      <div className={TILE_GRID}>
        <AppTileSkeleton />
        <AppTileSkeleton />
        <AppTileSkeleton />
      </div>
    );
  }

  if (error) {
    return <p className="text-sm text-destructive">{error}</p>;
  }

  if (apps.length === 0) {
    return (
      <Card className="flex min-h-40 flex-col items-center justify-center gap-2 p-6 text-center text-muted-foreground">
        <Boxes className="size-5 opacity-70" aria-hidden />
        <p className="text-sm font-medium">No apps yet</p>
        <p className="text-xs">Deploy an application to see it here.</p>
      </Card>
    );
  }

  return (
    <div className={TILE_GRID}>
      {apps.map((app, index) => {
        const code = statusByApp[app.id] ?? AppStatusCode.Unknown;
        const status = STATUS_STYLE[code] ?? STATUS_STYLE[AppStatusCode.Unknown];
        const unknown = code === AppStatusCode.Unknown;
        const named = Boolean(app.name?.trim());
        const label = appLabel(app.name, app.id);
        const routes = app.domains?.filter((d) => d.kind === "route") ?? [];
        return (
          <Link
            key={app.id}
            to={`/app/${app.id}`}
            title={`${label}: ${status.label}`}
            className="app-card-in block min-w-0"
            style={{ animationDelay: `${index * 40}ms` }}
          >
            <Card className="flex h-full items-center gap-3 p-3.5 transition-colors hover:border-primary/40">
              <AppIcon
                name={app.name}
                id={app.id}
                icon={app.icon}
                color={app.color}
                className="size-10 shrink-0 rounded-lg text-base"
              />
              <div className="min-w-0 flex-1">
                <div className="flex items-center justify-between gap-2">
                  <span
                    className={cn(
                      "truncate text-sm font-semibold tracking-tight",
                      !named && "font-mono font-medium text-muted-foreground",
                    )}
                  >
                    {label}
                  </span>
                  <span className="flex shrink-0 items-center gap-1.5 text-xs text-muted-foreground">
                    <span aria-hidden className={cn("size-2 shrink-0 rounded-full", status.dot)} />
                    {unknown ? null : status.label}
                  </span>
                </div>
                <div className="mt-0.5 flex min-w-0 gap-2 text-xs text-muted-foreground">
                  {routes.length === 0 ? (
                    <span className="truncate">{named ? "No public route" : "Not reconciled yet"}</span>
                  ) : null}
                  {/* Buttons, not anchors: the card is already a Link (<a>),
                      and nesting an <a> inside it is invalid interactive content. */}
                  {routes.slice(0, 2).map((d) => (
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
                      className="min-w-0 truncate hover:text-foreground hover:underline"
                    >
                      {d.domain}
                    </button>
                  ))}
                </div>
              </div>
            </Card>
          </Link>
        );
      })}
    </div>
  );
}
