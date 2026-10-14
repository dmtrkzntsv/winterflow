import { Link } from "react-router-dom";
import {
  ArrowUpCircle,
  DatabaseBackup,
  ExternalLink,
  LayoutTemplate,
  MoreHorizontal,
  Play,
  RotateCw,
  Square,
  Trash2,
} from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Spinner } from "@/components/ui/spinner";
import { AppIcon } from "@/components/app-icon";
import { StatusPill } from "@/components/status";
import type { App, ControlAction } from "@/context/apps-context-base";
import { hasRunningContainers } from "@/lib/app-status";
import { primaryAddress, type AppAddress } from "@/lib/app-summary";
import type { RegisteredDomain } from "@/lib/domains";
import { useHeadingInView } from "@/layouts/use-page-title";
import { appLabel } from "@/lib/kpi";
import { templatePath } from "@/lib/routes";
import { appStatus } from "@/lib/status";
import type { InstanceView } from "@/lib/templates";
import { cn } from "@/lib/utils";

export type AppHeaderProps = {
  // registrations are the server's registered domains: "Open ↗" uses https
  // for a host one covers.
  registrations?: RegisteredDomain[];
  // ports are the app's published ports on the server's IP (portAddresses):
  // the address to open when the app has no domain.
  ports?: AppAddress[];
  app: App;
  status: number | undefined;
  // failed: the app's deploy failed (it reads Failed).
  failed?: boolean;
  // instance is the template instance managing the app (null: a plain app,
  // or not read yet).
  instance: InstanceView | null;
  // behind: the template has a newer version than the app runs.
  behind: boolean;
  pending: ControlAction | null;
  onControl: (action: ControlAction) => void;
  onUpdateTemplate: () => void;
  backUpBusy: boolean;
  // progress says what a running action is doing ("Stopping…"), shown next
  // to the status; null when nothing runs.
  progress: string | null;
  onBackUpNow: () => void;
  canSaveAsTemplate: boolean;
  savingTemplate: boolean;
  onSaveAsTemplate: () => void;
  onDelete: () => void;
};

// AppHeader is the app page's header: who the app is (icon, name, status,
// where it opens, its template) and what to do now — one filled primary:
// Open ↗ while it runs and has an address (its first domain, else its
// first published port), Start while it doesn't run (an unknown status
// offers Start, which is harmless on a running stack), else Restart; the
// rest (Restart when Open is primary) in ⋯. On a phone the actions go
// under the name.
export function AppHeader(p: AppHeaderProps) {
  const { app } = p;
  const running = hasRunningContainers(p.status);
  const address = primaryAddress(app, p.registrations, p.ports);
  const open = running && address !== null;
  const named = Boolean(app.name?.trim());
  const h1 = useHeadingInView<HTMLHeadingElement>();
  return (
    <header className="flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
      <div className="flex min-w-0 items-center gap-4">
        <AppIcon name={app.name} icon={app.icon} color={app.color} className="size-14 shrink-0 rounded-xl text-xl" />
        <div className="min-w-0 space-y-1.5">
          <h1 ref={h1} className={cn("truncate text-2xl font-semibold tracking-tight", !named && "text-muted-foreground")}>
            {appLabel(app.name)}
          </h1>
          <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5 text-sm">
            <StatusPill status={appStatus(p.status, p.failed)} />
            {p.progress ? (
              <span role="status" className="inline-flex items-center gap-1.5 text-muted-foreground">
                <Spinner className="size-3.5" aria-hidden />
                {p.progress}
              </span>
            ) : null}
            {address ? (
              <a
                href={address.url}
                target="_blank"
                rel="noreferrer"
                className="inline-flex min-w-0 items-center gap-1.5 text-muted-foreground transition-colors duration-150 hover:text-foreground hover:underline"
              >
                <span className="truncate">{address.host}</span>
              </a>
            ) : null}
            {p.instance ? (
              <Link
                to={templatePath(p.instance.template_id)}
                className="rounded-full border px-2 py-0.5 text-xs font-medium transition-colors duration-150 hover:bg-muted"
              >
                From template {p.instance.template_name}
              </Link>
            ) : null}
            {p.instance?.state === "pending" ? <Badge variant="outline">Not created yet</Badge> : null}
            {p.behind ? (
              <Button
                size="sm"
                variant="outline"
                className="h-7 rounded-full border-status-warn/50 px-2.5 text-xs"
                onClick={p.onUpdateTemplate}
              >
                Update available
              </Button>
            ) : null}
          </div>
        </div>
      </div>
      <div className="flex shrink-0 items-center gap-2">
        {open ? (
          <Button asChild>
            <a href={address.url} target="_blank" rel="noreferrer">
              <ExternalLink className="size-4" aria-hidden />
              Open ↗
            </a>
          </Button>
        ) : running ? (
          <Button variant="outline" disabled={p.pending !== null} onClick={() => p.onControl("restart")}>
            {p.pending === "restart" ? <Spinner className="size-4" aria-hidden /> : <RotateCw className="size-4" aria-hidden />}
            Restart
          </Button>
        ) : (
          <Button disabled={p.pending !== null} onClick={() => p.onControl("start")}>
            {p.pending === "start" ? <Spinner className="size-4" aria-hidden /> : <Play className="size-4" aria-hidden />}
            Start
          </Button>
        )}
        <DropdownMenu modal={false}>
          <DropdownMenuTrigger asChild>
            <Button variant="outline" size="icon" aria-label="More actions">
              <MoreHorizontal className="size-4" />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            {open ? (
              <DropdownMenuItem disabled={p.pending !== null} onSelect={() => p.onControl("restart")}>
                {p.pending === "restart" ? <Spinner aria-hidden /> : <RotateCw aria-hidden />}
                Restart
              </DropdownMenuItem>
            ) : null}
            {running ? (
              <DropdownMenuItem disabled={p.pending !== null} onSelect={() => p.onControl("stop")}>
                <Square aria-hidden />
                Stop
              </DropdownMenuItem>
            ) : null}
            <DropdownMenuItem disabled={p.pending !== null} onSelect={() => p.onControl("update")}>
              <ArrowUpCircle aria-hidden />
              Pull latest images
            </DropdownMenuItem>
            <DropdownMenuItem disabled={p.backUpBusy} onSelect={p.onBackUpNow}>
              <DatabaseBackup aria-hidden />
              Back up now
            </DropdownMenuItem>
            {p.canSaveAsTemplate ? (
              <DropdownMenuItem disabled={p.savingTemplate} onSelect={p.onSaveAsTemplate}>
                <LayoutTemplate aria-hidden />
                Save as template
              </DropdownMenuItem>
            ) : null}
            <DropdownMenuSeparator />
            <DropdownMenuItem className="text-destructive focus:text-destructive" onSelect={p.onDelete}>
              <Trash2 aria-hidden />
              Delete…
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
    </header>
  );
}
