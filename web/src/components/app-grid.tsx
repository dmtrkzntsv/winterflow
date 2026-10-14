import { useState } from "react";
import { Link } from "react-router-dom";
import { Boxes, FileCode2, LayoutTemplate, TriangleAlert } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { AppIcon } from "@/components/app-icon";
import { EmptyState } from "@/components/empty-state";
import { StatusPill } from "@/components/status";
import type { App } from "@/context/apps-context-base";
import { useApps } from "@/context/use-apps";
import { useServerPath } from "@/context/use-server-path";
import { useServers } from "@/context/use-servers";
import { appWarning, filterApps, FILTER_THRESHOLD, primaryAddress, type AppWarning } from "@/lib/app-summary";
import { TILE_GRID } from "@/lib/grid";
import { appLabel } from "@/lib/kpi";
import { appSub, newAppSub } from "@/lib/routes";
import { appStatus } from "@/lib/status";
import { cn } from "@/lib/utils";

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

// NoApps is the Apps page of a server without apps: one sentence and the
// two most common ways to start.
function NoApps() {
  const toServer = useServerPath();
  return (
    <EmptyState
      icon={Boxes}
      title="Run your first app"
      action={
        <div className="flex flex-wrap justify-center gap-2">
          <Button asChild variant="outline">
            <Link to={toServer(newAppSub("template"))}>
              <LayoutTemplate className="size-4" aria-hidden /> From a template
            </Link>
          </Button>
          <Button asChild>
            <Link to={toServer(newAppSub("compose"))}>
              <FileCode2 className="size-4" aria-hidden /> Compose file
            </Link>
          </Button>
        </div>
      }
    />
  );
}

// AppCard is one app: icon, name (the link to the app, covering the card),
// its status, the address it opens at, and at most one warning. The address
// is its own external link above the card's.
function AppCard({ app, code, warning, index }: { app: App; code: number | undefined; warning: AppWarning | null; index: number }) {
  const toServer = useServerPath();
  const address = primaryAddress(app);
  const named = Boolean(app.name?.trim());
  return (
    <li className="app-card-in relative min-w-0" style={{ animationDelay: `${index * 40}ms` }}>
      <Card className="flex h-full items-center gap-3 p-3.5 transition-colors duration-150 hover:border-primary/40">
        <AppIcon name={app.name} icon={app.icon} color={app.color} className="size-10 shrink-0 rounded-lg text-base" />
        <div className="min-w-0 flex-1 space-y-1">
          <div className="flex items-center justify-between gap-2">
            <Link
              to={toServer(appSub(app.id, warning?.tab))}
              className={cn(
                "truncate text-sm font-semibold tracking-tight after:absolute after:inset-0 after:rounded-xl focus-visible:outline-none focus-visible:after:ring-2 focus-visible:after:ring-ring",
                !named && "font-medium text-muted-foreground",
              )}
            >
              {appLabel(app.name)}
            </Link>
            <StatusPill status={appStatus(code)} />
          </div>
          <div className="flex min-w-0 items-center gap-2 text-xs text-muted-foreground">
            {address ? (
              <a
                href={address.url}
                target="_blank"
                rel="noreferrer"
                className="relative z-10 truncate transition-colors duration-150 hover:text-foreground hover:underline"
              >
                {address.host}
              </a>
            ) : (
              <span className="truncate">No public address</span>
            )}
            {warning ? (
              <span className="ml-auto inline-flex shrink-0 items-center gap-1 rounded-full bg-status-warn/15 px-2 py-0.5 text-[11px] font-medium text-foreground">
                <TriangleAlert className="size-3 text-status-warn" aria-hidden />
                {warning.label}
              </span>
            ) : null}
          </div>
        </div>
      </Card>
    </li>
  );
}

// AppGrid is the active server's apps as cards (a filter box once there
// are more than FILTER_THRESHOLD), or the invitation to run the first one.
export function AppGrid() {
  const { apps, statusByApp, backupsByApp, staleVarsByApp, loading, error } = useApps();
  const { activeServerId } = useServers();
  const [query, setQuery] = useState("");
  // The page outlives a server switch: a query typed on one server is not
  // carried to the next.
  const [queryServer, setQueryServer] = useState(activeServerId);
  if (queryServer !== activeServerId) {
    setQueryServer(activeServerId);
    setQuery("");
  }

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
    return <NoApps />;
  }

  // Only an app list long enough to show the filter box is filtered by it.
  const filtering = apps.length > FILTER_THRESHOLD;
  const shown = filtering ? filterApps(apps, query) : apps;
  return (
    <div className="space-y-3">
      {filtering ? (
        <Input
          type="search"
          aria-label="Filter apps"
          placeholder="Filter apps"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          className="max-w-xs"
        />
      ) : null}
      {shown.length === 0 ? (
        <p className="text-sm text-muted-foreground">No app matches “{query.trim()}”.</p>
      ) : (
        <ul aria-label="Apps" className={TILE_GRID}>
          {shown.map((app, index) => (
            <AppCard
              key={app.id}
              app={app}
              code={statusByApp[app.id]}
              warning={appWarning(backupsByApp?.[app.id], staleVarsByApp?.[app.id])}
              index={index}
            />
          ))}
        </ul>
      )}
    </div>
  );
}
