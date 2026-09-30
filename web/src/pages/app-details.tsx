import { useCallback, useEffect, useMemo, useState } from "react";
import { useNavigate, useParams, useSearchParams } from "react-router-dom";
import {
  ArrowUpCircle,
  ExternalLink,
  History,
  Play,
  RefreshCw,
  RotateCw,
  Square,
  Trash2,
} from "lucide-react";
import { toast } from "sonner";

import { useAppBreadcrumbs } from "@/layouts/use-app-layout";
import { Button } from "@/components/ui/button";
import { buttonVariants } from "@/components/ui/button-variants";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Spinner } from "@/components/ui/spinner";
import { Input } from "@/components/ui/input";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import { AppIcon } from "@/components/app-icon";
import { LogsView, type LogLine } from "@/components/logs-view";
import { AppEditorPanel } from "@/components/app-editor-panel";
import { AppStatusIndicator } from "@/components/app-status-badge";
import { useApps } from "@/context/use-apps";
import { useNotifications } from "@/context/use-notifications";
import { useServers } from "@/context/use-servers";
import { apiBaseUrl } from "@/config";
import {
  AppStatusCode,
  type AppRevisions,
  type ControlAction,
} from "@/context/apps-context-base";
import { cn } from "@/lib/utils";

const base = apiBaseUrl.endsWith("/") ? apiBaseUrl.slice(0, -1) : apiBaseUrl;

const TABS = ["logs", "editor", "history", "settings"] as const;

const tabTriggerClass =
  "-mb-px rounded-none border-b-2 border-transparent px-1 pt-1 pb-2.5 text-muted-foreground shadow-none hover:text-foreground data-[state=active]:border-foreground data-[state=active]:bg-transparent data-[state=active]:text-foreground data-[state=active]:shadow-none";

export default function AppDetailsPage() {
  const { appId } = useParams();
  const navigate = useNavigate();
  const [searchParams, setSearchParams] = useSearchParams();
  const { apps, statusByApp, control, remove, rename } = useApps();

  const tabParam = searchParams.get("tab") ?? "logs";
  const tab = (TABS as readonly string[]).includes(tabParam)
    ? tabParam
    : "logs";
  const setTab = (next: string) => {
    setSearchParams(next === "logs" ? {} : { tab: next }, { replace: true });
  };

  const app = useMemo(() => apps.find((a) => a.id === appId), [apps, appId]);

  const breadcrumbs = useMemo(
    () => [{ label: "Apps", href: "/" }, { label: app?.name ?? appId ?? "App" }],
    [app?.name, appId],
  );
  useAppBreadcrumbs(breadcrumbs);

  const [pending, setPending] = useState<ControlAction | null>(null);

  const runControl = async (action: ControlAction) => {
    if (!appId) return;
    setPending(action);
    try {
      await control(appId, action);
      toast.success(CONTROL_DONE[action]);
    } catch (e) {
      toast.error(`Couldn't ${action} the app`, {
        description: e instanceof Error ? e.message : undefined,
      });
    } finally {
      setPending(null);
    }
  };

  if (!app) {
    return (
      <div className="flex min-h-60 items-center justify-center">
        <p className="text-sm text-muted-foreground">App not found.</p>
      </div>
    );
  }

  const status = statusByApp[app.id];
  // Unknown status offers Start too: we never infer "down" from missing data,
  // but starting an already-running stack is harmless.
  const canStop =
    status !== undefined &&
    status !== AppStatusCode.Stopped &&
    status !== AppStatusCode.Unknown;
  const routes = (app.domains ?? []).filter((d) => d.kind === "route");

  const controlButton = (
    action: ControlAction,
    label: string,
    Icon: typeof Play,
    title?: string,
  ) => (
    <Button
      variant="outline"
      size="sm"
      disabled={pending !== null}
      title={title}
      onClick={() => void runControl(action)}
    >
      {pending === action ? (
        <Spinner className="size-4" />
      ) : (
        <Icon className="size-4" />
      )}
      {label}
    </Button>
  );

  return (
    <div className="space-y-6">
      <header className="flex flex-wrap items-center gap-x-4 gap-y-3">
        <AppIcon
          name={app.name}
          icon={app.icon}
          color={app.color}
          className="size-14 shrink-0 rounded-xl text-xl"
        />
        <div className="min-w-0 flex-1">
          <h1 className="truncate text-2xl font-semibold tracking-tight">
            {app.name}
          </h1>
          <div className="mt-1 flex flex-wrap items-center gap-x-4 gap-y-1 text-sm text-muted-foreground">
            <AppStatusIndicator status={status} />
            {app.version ? <span>Revision {app.version}</span> : null}
            {routes.slice(0, 2).map((d) => (
              <a
                key={d.domain}
                href={`${d.ssl ? "https" : "http"}://${d.domain}`}
                target="_blank"
                rel="noreferrer"
                className="inline-flex items-center gap-1 hover:text-foreground hover:underline"
              >
                {d.domain}
                <ExternalLink className="size-3" />
              </a>
            ))}
          </div>
        </div>
        <div className="flex flex-wrap gap-2">
          {canStop
            ? controlButton("stop", "Stop", Square)
            : controlButton("start", "Start", Play)}
          {controlButton("restart", "Restart", RotateCw)}
          {controlButton(
            "update",
            "Update",
            ArrowUpCircle,
            "Pull newer images and recreate the containers",
          )}
        </div>
      </header>

      <Tabs value={tab} onValueChange={setTab}>
        <TabsList className="h-auto w-full justify-start gap-6 rounded-none border-b bg-transparent p-0">
          <TabsTrigger value="logs" className={tabTriggerClass}>
            Logs
          </TabsTrigger>
          <TabsTrigger value="editor" className={tabTriggerClass}>
            Configuration
          </TabsTrigger>
          <TabsTrigger value="history" className={tabTriggerClass}>
            History
          </TabsTrigger>
          <TabsTrigger value="settings" className={tabTriggerClass}>
            Settings
          </TabsTrigger>
        </TabsList>
        <TabsContent value="logs" className="mt-6">
          <LogsTab appId={app.id} />
        </TabsContent>
        <TabsContent value="editor" className="mt-0">
          <AppEditorPanel appId={app.id} />
        </TabsContent>
        <TabsContent value="history" className="mt-6">
          <HistoryTab appId={app.id} />
        </TabsContent>
        <TabsContent value="settings" className="mt-6">
          <SettingsTab
            appId={app.id}
            name={app.name}
            onRename={rename}
            onDelete={async () => {
              await remove(app.id);
              navigate("/");
            }}
          />
        </TabsContent>
      </Tabs>
    </div>
  );
}

const CONTROL_DONE: Record<ControlAction, string> = {
  start: "App started",
  stop: "App stopped",
  restart: "App restarted",
  update: "App updated",
};

function LogsTab({ appId }: { appId: string }) {
  const { activeServerId } = useServers();
  const { waitFor } = useNotifications();
  const [logs, setLogs] = useState<LogLine[]>([]);
  const [tail, setTail] = useState<number>(200);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const fetchLogs = useCallback(async () => {
    if (!activeServerId) return;
    setLoading(true);
    setError(null);
    try {
      const url = `${base}/api/v1/app/get-logs?server_id=${encodeURIComponent(
        activeServerId,
      )}&app_id=${encodeURIComponent(appId)}&tail=${tail}`;
      const res = await fetch(url, { credentials: "include" });
      if (!res.ok) throw new Error(`Request failed: ${res.status}`);
      const accepted = (await res.json()) as { data?: { request_id?: string } };
      const ref = accepted.data?.request_id;
      if (!ref) throw new Error("No request id");
      const result = await waitFor(ref);
      if (result.status && result.status !== 0) {
        throw new Error(result.error || "Failed to fetch logs");
      }
      const payload = result.payload as { logs?: LogLine[] } | undefined;
      setLogs(payload?.logs ?? []);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Failed to fetch logs");
    } finally {
      setLoading(false);
    }
  }, [appId, activeServerId, waitFor, tail]);

  useEffect(() => {
    void fetchLogs();
  }, [fetchLogs]);

  return (
    <LogsView
      lines={logs}
      loading={loading}
      error={error}
      tail={tail}
      onTailChange={setTail}
      onRefresh={() => void fetchLogs()}
    />
  );
}

function HistoryTab({ appId }: { appId: string }) {
  const { getRevisions, rollback, control } = useApps();
  const [data, setData] = useState<AppRevisions | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      setData(await getRevisions(appId));
    } catch (e) {
      setError(e instanceof Error ? e.message : "Failed to load history");
    } finally {
      setLoading(false);
    }
  }, [appId, getRevisions]);

  useEffect(() => {
    void load();
  }, [load]);

  const doRollback = async (hash: string) => {
    setBusy(true);
    try {
      await rollback(appId, hash);
      toast.success("Rolled back and redeployed");
      await load();
    } catch (e) {
      toast.error("Roll back failed", {
        description: e instanceof Error ? e.message : undefined,
      });
    } finally {
      setBusy(false);
    }
  };

  // Deploying the current draft is a plain start: the worktree already holds
  // HEAD, so materialize + compose up brings the draft live.
  const doDeploy = async () => {
    setBusy(true);
    try {
      await control(appId, "start");
      toast.success("Draft deployed");
      await load();
    } catch (e) {
      toast.error("Deploy failed", {
        description: e instanceof Error ? e.message : undefined,
      });
    } finally {
      setBusy(false);
    }
  };

  if (loading && !data) {
    return (
      <div className="flex h-40 items-center justify-center">
        <Spinner />
      </div>
    );
  }
  if (error) {
    return (
      <div className="flex h-40 flex-col items-center justify-center gap-3">
        <p className="text-sm text-destructive">{error}</p>
        <Button size="sm" variant="outline" onClick={() => void load()}>
          Try again
        </Button>
      </div>
    );
  }
  if (!data || data.revisions.length === 0) {
    return (
      <p className="py-10 text-center text-sm text-muted-foreground">
        No history yet. Every save of the configuration shows up here.
      </p>
    );
  }

  return (
    <div className="max-w-4xl">
      <div className="mb-4 flex items-center justify-between">
        <p className="text-sm text-muted-foreground">
          {data.revisions.length === 1
            ? "1 revision"
            : `${data.revisions.length} revisions`}
          , newest first. Rolling back adds a new revision; nothing is lost.
        </p>
        <Button
          size="icon"
          variant="ghost"
          className="size-8 text-muted-foreground"
          aria-label="Refresh history"
          title="Refresh history"
          onClick={() => void load()}
          disabled={loading}
        >
          <RefreshCw className={cn("size-4", loading && "animate-spin")} />
        </Button>
      </div>
      <ol>
        {data.revisions.map((rev, i) => {
          const isCurrent = rev.hash === data.current;
          const isDeployed = data.deployed !== "" && rev.hash === data.deployed;
          const isUndeployedDraft = isCurrent && data.deployed !== "" && !isDeployed;
          const isLast = i === data.revisions.length - 1;
          const when = new Date(rev.timestamp * 1000);
          return (
            <li key={rev.hash} className="relative flex gap-4 pb-6 last:pb-0">
              {/* The rail: a dot per revision joined by a line. */}
              {!isLast ? (
                <span
                  className="absolute top-4 bottom-0 left-[7px] w-px bg-border"
                  aria-hidden
                />
              ) : null}
              <span
                className={cn(
                  "relative mt-1 size-[15px] shrink-0 rounded-full border-2 bg-background",
                  isDeployed
                    ? "border-emerald-500 bg-emerald-500 ring-4 ring-emerald-500/15"
                    : isUndeployedDraft
                      ? "border-amber-400"
                      : "border-muted-foreground/40",
                )}
                aria-hidden
              />
              <div className="flex min-w-0 flex-1 flex-wrap items-start gap-x-4 gap-y-2">
                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="truncate text-sm font-medium">
                      {rev.subject}
                    </span>
                    {isDeployed ? (
                      <span className="text-xs font-medium text-emerald-700 dark:text-emerald-400">
                        Live
                      </span>
                    ) : null}
                    {isUndeployedDraft ? (
                      <span className="text-xs font-medium text-amber-700 dark:text-amber-400">
                        Draft, not deployed
                      </span>
                    ) : null}
                  </div>
                  <div className="mt-0.5 flex items-center gap-3 text-xs text-muted-foreground">
                    <code className="font-mono">{rev.hash.slice(0, 8)}</code>
                    <time dateTime={when.toISOString()} title={when.toLocaleString()}>
                      {relativeTime(when)}
                    </time>
                  </div>
                </div>
                {isUndeployedDraft ? (
                  <Button size="sm" onClick={() => void doDeploy()} disabled={busy}>
                    Deploy draft
                  </Button>
                ) : !isCurrent ? (
                  <AlertDialog>
                    <AlertDialogTrigger asChild>
                      <Button size="sm" variant="ghost" disabled={busy}>
                        <History className="size-4" /> Roll back
                      </Button>
                    </AlertDialogTrigger>
                    <AlertDialogContent>
                      <AlertDialogHeader>
                        <AlertDialogTitle>
                          Roll back to {rev.hash.slice(0, 8)}?
                        </AlertDialogTitle>
                        <AlertDialogDescription>
                          The app's files and variables are restored to this
                          revision as a new history entry, and the app is
                          redeployed. Nothing is lost — you can roll forward
                          again.
                        </AlertDialogDescription>
                      </AlertDialogHeader>
                      <AlertDialogFooter>
                        <AlertDialogCancel>Cancel</AlertDialogCancel>
                        <AlertDialogAction onClick={() => void doRollback(rev.hash)}>
                          Roll back
                        </AlertDialogAction>
                      </AlertDialogFooter>
                    </AlertDialogContent>
                  </AlertDialog>
                ) : null}
              </div>
            </li>
          );
        })}
      </ol>
    </div>
  );
}

const rtf = new Intl.RelativeTimeFormat(undefined, { numeric: "auto" });

// relativeTime renders "5 minutes ago"-style labels, falling back to the date
// once a revision is more than a week old.
function relativeTime(d: Date): string {
  const secs = Math.round((d.getTime() - Date.now()) / 1000);
  const abs = Math.abs(secs);
  if (abs < 60) return rtf.format(secs, "second");
  if (abs < 3600) return rtf.format(Math.round(secs / 60), "minute");
  if (abs < 86400) return rtf.format(Math.round(secs / 3600), "hour");
  if (abs < 7 * 86400) return rtf.format(Math.round(secs / 86400), "day");
  return d.toLocaleDateString();
}

function SettingsTab({
  appId,
  name,
  onRename,
  onDelete,
}: {
  appId: string;
  name: string;
  onRename: (appId: string, name: string) => Promise<void>;
  onDelete: () => Promise<void>;
}) {
  const [newName, setNewName] = useState(name);
  const [busy, setBusy] = useState(false);

  const save = async () => {
    setBusy(true);
    try {
      await onRename(appId, newName.trim());
      toast.success("App renamed");
    } catch (e) {
      toast.error("Rename failed", {
        description: e instanceof Error ? e.message : undefined,
      });
    } finally {
      setBusy(false);
    }
  };

  const del = async () => {
    setBusy(true);
    try {
      await onDelete();
      toast.success("App deleted");
    } catch (e) {
      toast.error("Delete failed", {
        description: e instanceof Error ? e.message : undefined,
      });
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="max-w-4xl divide-y rounded-lg border">
      <div className="grid gap-4 p-5 md:grid-cols-[14rem_minmax(0,1fr)] md:gap-8">
        <div className="space-y-1">
          <h2 className="text-sm font-semibold">Name</h2>
          <p className="text-sm text-muted-foreground">
            Shown in the sidebar and app list. The app's containers keep running.
          </p>
        </div>
        <form
          className="flex items-center gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            void save();
          }}
        >
          <Input
            id="app-name"
            aria-label="App name"
            value={newName}
            onChange={(e) => setNewName(e.target.value)}
          />
          <Button
            type="submit"
            disabled={busy || !newName.trim() || newName === name}
          >
            Rename
          </Button>
        </form>
      </div>

      <div className="grid gap-4 p-5 md:grid-cols-[14rem_minmax(0,1fr)] md:gap-8">
        <div className="space-y-1">
          <h2 className="text-sm font-semibold text-destructive">Delete app</h2>
          <p className="text-sm text-muted-foreground">
            Stops the containers and removes the deployment and its history.
            Named Docker volumes are kept.
          </p>
        </div>
        <div>
          <AlertDialog>
            <AlertDialogTrigger asChild>
              <Button variant="outline" className="border-destructive/40 text-destructive hover:bg-destructive/10 hover:text-destructive" disabled={busy}>
                <Trash2 className="size-4" /> Delete {name}
              </Button>
            </AlertDialogTrigger>
            <AlertDialogContent>
              <AlertDialogHeader>
                <AlertDialogTitle>Delete {name}?</AlertDialogTitle>
                <AlertDialogDescription>
                  This stops the app's containers and removes its deployment and
                  stored revisions. This cannot be undone.
                </AlertDialogDescription>
              </AlertDialogHeader>
              <AlertDialogFooter>
                <AlertDialogCancel>Cancel</AlertDialogCancel>
                <AlertDialogAction
                  className={buttonVariants({ variant: "destructive" })}
                  onClick={() => void del()}
                >
                  Delete
                </AlertDialogAction>
              </AlertDialogFooter>
            </AlertDialogContent>
          </AlertDialog>
        </div>
      </div>
    </div>
  );
}
