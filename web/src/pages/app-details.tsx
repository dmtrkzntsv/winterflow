import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router-dom";
import {
  ArrowUpCircle,
  ExternalLink,
  History,
  Play,
  RefreshCw,
  RotateCw,
  Square,
  TriangleAlert,
} from "lucide-react";
import { toast } from "sonner";

import { useAppBreadcrumbs } from "@/layouts/use-app-layout";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
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
import { AppBackups } from "@/components/app-backups";
import { AppEditorPanel } from "@/components/app-editor-panel";
import { ErrorText } from "@/components/error-text";
import { InlineError } from "@/components/inline-error";
import { InstancePanel } from "@/components/instance-panel";
import { UpgradeInstanceDialog } from "@/components/upgrade-instance-dialog";
import { AppStatusIndicator } from "@/components/app-status-badge";
import { DeleteAppDialog } from "@/components/delete-app-dialog";
import { AppAlertsBadge } from "@/components/alerts/active-alerts";
import { AppMetricBlocks, AppMetricsTab } from "@/components/app-metrics";
import { StaleVarsBadge } from "@/components/stale-vars-badge";
import { useApps } from "@/context/use-apps";
import { useNotifications } from "@/context/use-notifications";
import { useServers } from "@/context/use-servers";
import { apiBaseUrl } from "@/config";
import {
  AppStatusCode,
  type AppRevisions,
  type ControlAction,
  type DeleteAppResult,
} from "@/context/apps-context-base";
import { useAppInstance } from "@/hooks/use-app-instance";
import { useTemplatesApi } from "@/hooks/use-templates-api";
import { readApiError } from "@/lib/api";
import { needsAttention } from "@/lib/backups";
import { deleteOutcome, type DeleteChoice } from "@/lib/delete-app";
import { appLabel } from "@/lib/kpi";
import { templateFromApp } from "@/lib/template-draft";
import { isManagedByTemplate, type InstanceView, type TemplateView } from "@/lib/templates";
import type { DraftState } from "@/pages/template-page";
import { cn, relativeTime } from "@/lib/utils";
import { useServerPath } from "@/context/use-server-path";
import { NotFound } from "@/components/not-found";

const base = apiBaseUrl.endsWith("/") ? apiBaseUrl.slice(0, -1) : apiBaseUrl;

const TABS = ["logs", "metrics", "editor", "backups", "history", "settings"] as const;

const tabTriggerClass =
  "-mb-px rounded-none border-b-2 border-transparent px-0.5 pt-1 sm:px-1 pb-2.5 text-muted-foreground shadow-none hover:text-foreground data-[state=active]:border-foreground data-[state=active]:bg-transparent data-[state=active]:text-foreground data-[state=active]:shadow-none";

export default function AppDetailsPage() {
  const { appId } = useParams();
  const navigate = useNavigate();
  const toServer = useServerPath();
  const [searchParams, setSearchParams] = useSearchParams();
  const { apps, statusByApp, backupsByApp, loading, control, remove, rename, refresh } = useApps();
  const templatesApi = useTemplatesApi();

  const tabParam = searchParams.get("tab") ?? "logs";
  const tab = (TABS as readonly string[]).includes(tabParam)
    ? tabParam
    : "logs";
  const setTab = (next: string) => {
    setSearchParams(next === "logs" ? {} : { tab: next }, { replace: true });
  };
  // A tab opened by link may sit past the bar's scrolled edge on a phone.
  const tabBar = useRef<HTMLDivElement | null>(null);
  useEffect(() => {
    tabBar.current
      ?.querySelector<HTMLElement>('[role="tab"][data-state="active"]')
      ?.scrollIntoView?.({ block: "nearest", inline: "nearest" });
  }, [tab]);

  const app = useMemo(() => apps.find((a) => a.id === appId), [apps, appId]);
  // managed is the template instance behind the app, when there is one.
  const managed = useAppInstance(app);
  // Only this app's own instance is ever used (the page stays mounted when
  // the user moves from app to app).
  const inst = app?.templateId && managed.instance?.app_id === app.id ? managed.instance : null;
  const latest = managed.current?.latest_revision ?? 0;
  const behind = inst !== null && latest > inst.revision;
  const [upgrade, setUpgrade] = useState<{ appId: string; target: TemplateView } | null>(null);
  const upgradeTarget = upgrade && upgrade.appId === app?.id ? upgrade.target : null;
  const reloadManaged = () => void Promise.all([managed.reload(), refresh()]);
  const openUpgrade = async () => {
    if (!inst) return;
    try {
      setUpgrade({ appId: inst.app_id, target: await templatesApi.getTemplate(inst.template_id) });
    } catch (e) {
      toast.error("Couldn't load the template", { description: e instanceof Error ? e.message : undefined });
    }
  };

  const breadcrumbs = useMemo(
    () => [
      { label: "Apps", href: toServer() },
      { label: appId ? appLabel(app?.name, appId) : "App" },
    ],
    [app?.name, appId, toServer],
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
    if (loading) return null;
    return (
      <NotFound
        title="App not found"
        description="This app doesn't exist on this server."
      />
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
          id={app.id}
          icon={app.icon}
          color={app.color}
          className="size-14 shrink-0 rounded-xl text-xl"
        />
        <div className="min-w-0 flex-1">
          {/* An app not reconciled yet has no name: its id prefix stands in. */}
          <h1
            className={cn(
              "truncate text-2xl font-semibold tracking-tight",
              !app.name?.trim() && "font-mono text-muted-foreground",
            )}
          >
            {appLabel(app.name, app.id)}
          </h1>
          <div className="mt-1 flex flex-wrap items-center gap-x-4 gap-y-1 text-sm text-muted-foreground">
            <AppStatusIndicator status={status} />
            <AppAlertsBadge serverId={app.serverId} appId={app.id} />
            {inst ? (
              <span className="inline-flex items-center gap-2">
                <Link
                  to={`/org/templates/${inst.template_id}`}
                  className="rounded-md border px-1.5 py-0.5 text-xs font-medium text-foreground hover:bg-muted"
                >
                  {inst.template_name} · r{inst.revision}
                </Link>
                {behind ? (
                  <span className="text-xs text-amber-700 dark:text-amber-400">r{latest} available</span>
                ) : null}
                {inst.state === "pending" ? <Badge variant="outline">Not created yet</Badge> : null}
              </span>
            ) : null}
            {/* A templated app's revision is its template's (the badge);
                its own save count stays in History. */}
            {app.version && !app.templateId ? <span>Revision {app.version}</span> : null}
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
          {behind ? (
            <Button size="sm" onClick={() => void openUpgrade()}>
              Upgrade to r{latest}
            </Button>
          ) : null}
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

      {inst?.state === "pending" && inst.last_error ? (
        <ErrorText
          alert
          icon
          text={inst.last_error}
          prefix="The app's last deploy from its template failed: "
          suffix=". Fix its values under Configuration and save again."
        />
      ) : null}

      {upgradeTarget && inst ? (
        <UpgradeInstanceDialog
          // The page's live status says whether the app is stopped now.
          instance={{ ...inst, status_code: status ?? inst.status_code }}
          appName={appLabel(app.name, app.id)}
          current={managed.current}
          target={upgradeTarget}
          open
          onOpenChange={(o) => {
            if (!o) setUpgrade(null);
          }}
          onDone={reloadManaged}
        />
      ) : null}

      <StaleVarsBadge key={app.id} appId={app.id} />

      <AppMetricBlocks serverId={app.serverId} appId={app.id} />

      <Tabs value={tab} onValueChange={setTab}>
        {/* Six tabs are wider than a phone: the bar scrolls inside itself
            instead of widening the page. */}
        <div ref={tabBar} className="-mx-1 max-w-full overflow-x-auto px-1">
          <TabsList className="h-auto w-max min-w-full justify-start gap-6 rounded-none border-b bg-transparent p-0">
            <TabsTrigger value="logs" className={tabTriggerClass}>
              Logs
            </TabsTrigger>
            <TabsTrigger value="metrics" className={tabTriggerClass}>
              Metrics
            </TabsTrigger>
            <TabsTrigger value="editor" className={tabTriggerClass}>
              Configuration
            </TabsTrigger>
            <TabsTrigger value="backups" className={tabTriggerClass}>
              Backups
              {needsAttention(backupsByApp?.[app.id]) ? (
                <TriangleAlert
                  role="img"
                  aria-label="A backup is late or failing"
                  className="ml-1.5 inline size-3.5 align-text-bottom text-amber-500"
                />
              ) : null}
            </TabsTrigger>
            <TabsTrigger value="history" className={tabTriggerClass}>
              History
            </TabsTrigger>
            <TabsTrigger value="settings" className={tabTriggerClass}>
              Settings
            </TabsTrigger>
          </TabsList>
        </div>
        <TabsContent value="logs" className="mt-6">
          <LogsTab appId={app.id} />
        </TabsContent>
        <TabsContent value="metrics" className="mt-6">
          <AppMetricsTab serverId={app.serverId} appId={app.id} appName={appLabel(app.name, app.id)} />
        </TabsContent>
        <TabsContent value="editor" className="mt-0">
          {app.templateId && managed.error ? (
            <div className="flex min-h-60 flex-col items-center justify-center gap-3">
              <InlineError>{managed.error}</InlineError>
              <Button variant="outline" size="sm" onClick={() => void managed.reload()}>
                Retry
              </Button>
            </div>
          ) : app.templateId && managed.loading ? (
            <div className="flex min-h-60 items-center justify-center">
              <Spinner />
            </div>
          ) : inst && managed.current ? (
            <InstancePanel
              key={`${inst.revision}-${inst.updated_at}`}
              instance={inst}
              current={managed.current}
              onSaved={reloadManaged}
            />
          ) : (
            <AppEditorPanel appId={app.id} />
          )}
        </TabsContent>
        <TabsContent value="backups" className="mt-6">
          <AppBackups appId={app.id} />
        </TabsContent>
        <TabsContent value="history" className="mt-6">
          <HistoryTab appId={app.id} managed={Boolean(app.templateId)} managedBy={inst?.template_name} />
        </TabsContent>
        <TabsContent value="settings" className="mt-6">
          <SettingsTab
            appId={app.id}
            name={app.name}
            instance={inst}
            managed={Boolean(app.templateId)}
            onUnlinked={reloadManaged}
            onRename={rename}
            onDelete={async (opts) => {
              const result = await remove(app.id, opts);
              navigate(toServer());
              return result;
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
      if (!res.ok) throw await readApiError(res);
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

// managed: a template owns the app (its instance may still be loading, so
// managedBy, the template's name, can be unknown).
function HistoryTab({ appId, managed, managedBy }: { appId: string; managed: boolean; managedBy?: string }) {
  const { getRevisions, rollback, control, refresh } = useApps();
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
      // A template took the app over meanwhile: show it as managed.
      if (e instanceof Error && isManagedByTemplate(e.message)) void refresh();
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
          , newest first.{" "}
          {managed
            ? `Managed by ${managedBy ? `template ${managedBy}` : "a template"}: it changes through its parameters or an upgrade; unlink it to roll back.`
            : "Rolling back adds a new revision; nothing is lost."}
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
                ) : !isCurrent && !managed ? (
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

function SettingsTab({
  appId,
  name,
  instance,
  managed,
  onUnlinked,
  onRename,
  onDelete,
}: {
  appId: string;
  name: string;
  instance: InstanceView | null;
  // managed: a template owns the app (its instance may still be loading), so
  // it can't be offered as the source of a new template.
  managed: boolean;
  onUnlinked: () => void;
  onRename: (appId: string, name: string) => Promise<void>;
  onDelete: (opts: { deleteVolumes: boolean; deleteBackups: boolean }) => Promise<DeleteAppResult>;
}) {
  const { backupsByApp, getApp } = useApps();
  const navigate = useNavigate();
  const templatesApi = useTemplatesApi();
  const [newName, setNewName] = useState(name);
  const [busy, setBusy] = useState(false);
  const [deleting, setDeleting] = useState(false);

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

  const unlink = async () => {
    if (!instance) return;
    setBusy(true);
    try {
      await templatesApi.unlinkInstance(instance.server_id, instance.key);
      toast.success("Unlinked from the template");
      onUnlinked();
    } catch (e) {
      toast.error("Unlink failed", { description: e instanceof Error ? e.message : undefined });
    } finally {
      setBusy(false);
    }
  };

  // saveAsTemplate opens a new template's editor prefilled from what the
  // app's editor holds; nothing is saved until the template is.
  const saveAsTemplate = async () => {
    setBusy(true);
    try {
      const state: DraftState = { ...templateFromApp(appId, await getApp(appId)), appName: appLabel(name, appId) };
      navigate("/org/templates/new", { state });
    } catch (e) {
      toast.error("Couldn't read the app", { description: e instanceof Error ? e.message : undefined });
      setBusy(false);
    }
  };

  const del = async (choice: DeleteChoice) => {
    setBusy(true);
    setDeleting(true);
    try {
      const hadBackups = (backupsByApp?.[appId]?.length ?? 0) > 0;
      const res = await onDelete({ deleteVolumes: choice.withVolumes, deleteBackups: choice.withBackups });
      const out = deleteOutcome(choice, res, { hadBackups });
      if (out.level === "warning") {
        toast.warning(out.title, { description: out.description });
      } else {
        toast.success(out.title, { description: out.description });
      }
    } catch (e) {
      toast.error("Delete failed", {
        description: e instanceof Error ? e.message : undefined,
      });
    } finally {
      setBusy(false);
      setDeleting(false);
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

      {instance ? (
        <div className="grid gap-4 p-5 md:grid-cols-[14rem_minmax(0,1fr)] md:gap-8">
          <div className="space-y-1">
            <h2 className="text-sm font-semibold">Template</h2>
            <p className="text-sm text-muted-foreground">
              Managed by template {instance.template_name}: its files and values change through the template.
            </p>
          </div>
          {instance.state !== "created" ? (
            // The API unlinks only a created instance.
            <p className="text-sm text-muted-foreground">
              Unlink becomes available once its first deploy from the template succeeds.
            </p>
          ) : (
            <div>
              <AlertDialog>
                <AlertDialogTrigger asChild>
                  <Button variant="outline" disabled={busy}>
                    Unlink from template
                  </Button>
                </AlertDialogTrigger>
                <AlertDialogContent>
                  <AlertDialogHeader>
                    <AlertDialogTitle>Unlink from {instance.template_name}?</AlertDialogTitle>
                    <AlertDialogDescription>
                      The app becomes an ordinary app you can edit freely; template upgrades no longer reach it.
                    </AlertDialogDescription>
                  </AlertDialogHeader>
                  <AlertDialogFooter>
                    <AlertDialogCancel>Cancel</AlertDialogCancel>
                    <AlertDialogAction onClick={() => void unlink()}>Unlink</AlertDialogAction>
                  </AlertDialogFooter>
                </AlertDialogContent>
              </AlertDialog>
            </div>
          )}
        </div>
      ) : managed ? null : (
        <div className="grid gap-4 p-5 md:grid-cols-[14rem_minmax(0,1fr)] md:gap-8">
          <div className="space-y-1">
            <h2 className="text-sm font-semibold">Save as template</h2>
            <p className="text-sm text-muted-foreground">
              Start a template from this app: its files, its variables as parameters, its hosts and backup settings.
              The app stays as it is.
            </p>
          </div>
          <div>
            <Button variant="outline" disabled={busy} onClick={() => void saveAsTemplate()}>
              Save as template
            </Button>
          </div>
        </div>
      )}

      <div className="grid gap-4 p-5 md:grid-cols-[14rem_minmax(0,1fr)] md:gap-8">
        <div className="space-y-1">
          <h2 className="text-sm font-semibold text-destructive">Delete app</h2>
          <p className="text-sm text-muted-foreground">
            Stops the containers and removes the deployment and its history.
            Volumes and backups are kept unless you choose otherwise.
          </p>
        </div>
        <div>
          <DeleteAppDialog
            name={name}
            disabled={busy}
            deleting={deleting}
            onConfirm={(choice) => void del(choice)}
          />
        </div>
      </div>
    </div>
  );
}
