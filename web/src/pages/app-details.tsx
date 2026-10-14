import { useMemo, useState, type ReactNode } from "react";
import { Link, useParams } from "react-router-dom";
import {
  ArrowUpCircle,
  ExternalLink,
  History,
  Play,
  RotateCw,
  ScrollText,
  Square,
  TriangleAlert,
} from "lucide-react";
import { toast } from "sonner";

import type { PaletteAction } from "@/context/palette-context-base";
import { usePaletteActions } from "@/context/use-palette";
import { usePageTitle } from "@/layouts/use-page-title";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { TabsContent } from "@/components/ui/tabs";
import { UnderlineTabs } from "@/components/underline-tabs";
import { useDeleteApp, useSaveAsTemplate } from "@/hooks/use-app-actions";
import { useHistorySheet } from "@/hooks/use-history-sheet";
import { useTabParam } from "@/hooks/use-tab-param";
import { LEGACY_APP_TABS, templatePath } from "@/lib/routes";
import { Spinner } from "@/components/ui/spinner";
import { AppLogsTab } from "@/components/app-page/app-logs-tab";
import { DeleteAppDialog } from "@/components/delete-app-dialog";
import { AppSettingsTab } from "@/components/app-page/app-settings-tab";
import { HistorySheet } from "@/components/app-page/history-sheet";
import type { Ownership } from "@/components/app-page/ownership";
import { AppIcon } from "@/components/app-icon";
import { AppBackups } from "@/components/app-backups";
import { AppEditorPanel } from "@/components/app-editor-panel";
import { ErrorText } from "@/components/error-text";
import { InstancePanel } from "@/components/instance-panel";
import { LoadError, PageSpinner } from "@/components/page-states";
import { UpgradeInstanceDialog } from "@/components/upgrade-instance-dialog";
import { AppStatusIndicator } from "@/components/app-status-badge";
import { AppAlertsBadge } from "@/components/alerts/active-alerts";
import { AppMetricBlocks, AppMetricsTab } from "@/components/app-metrics";
import { StaleVarsBadge } from "@/components/stale-vars-badge";
import { useApps } from "@/context/use-apps";
import {
  AppStatusCode,
  type ControlAction,
} from "@/context/apps-context-base";
import { useAppInstance } from "@/hooks/use-app-instance";
import { useTemplatesApi } from "@/hooks/use-templates-api";
import { needsAttention } from "@/lib/backups";
import { appLabel } from "@/lib/kpi";
import type { TemplateView } from "@/lib/templates";
import { cn } from "@/lib/utils";
import { NotFound } from "@/components/not-found";

const TABS = ["logs", "metrics", "configuration", "backups", "settings"] as const;
type Tab = (typeof TABS)[number];

export default function AppDetailsPage() {
  const { appId } = useParams();
  const { apps, statusByApp, backupsByApp, loading, control, rename, refresh } = useApps();
  const templatesApi = useTemplatesApi();

  const [tab, setTab] = useTabParam<Tab>(TABS, "logs", { aliases: LEGACY_APP_TABS });
  const [historyOpen, setHistoryOpen] = useHistorySheet();

  const app = useMemo(() => apps.find((a) => a.id === appId), [apps, appId]);
  const lookup = useAppInstance(app);
  // ownership is the template side of the app (null for a plain app). Only
  // this app's own instance is ever used (the page stays mounted when the
  // user moves from app to app).
  const ownership: Ownership = app?.templateId
    ? { ...lookup, instance: lookup.instance?.app_id === app.id ? lookup.instance : null }
    : null;
  const inst = ownership?.instance ?? null;
  const latest = ownership?.current?.latest_revision ?? 0;
  const behind = inst !== null && latest > inst.revision;
  const [upgrade, setUpgrade] = useState<{ appId: string; target: TemplateView } | null>(null);
  const upgradeTarget = upgrade && upgrade.appId === app?.id ? upgrade.target : null;
  const reloadManaged = () => void Promise.all([lookup.reload(), refresh()]);
  const openUpgrade = async () => {
    if (!inst) return;
    try {
      setUpgrade({ appId: inst.app_id, target: await templatesApi.getTemplate(inst.template_id) });
    } catch (e) {
      toast.error("Couldn't load the template", { description: e instanceof Error ? e.message : undefined });
    }
  };

  usePageTitle(appId ? appLabel(app?.name, appId) : "App");

  const [pending, setPending] = useState<ControlAction | null>(null);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const deleteApp = useDeleteApp(appId ?? "");
  const saveAsTemplate = useSaveAsTemplate(appId ?? "", app?.name ?? "");

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

  const status = app ? statusByApp[app.id] : undefined;
  // Unknown status offers Start too: we never infer "down" from missing data,
  // but starting an already-running stack is harmless.
  const canStop =
    status !== undefined &&
    status !== AppStatusCode.Stopped &&
    status !== AppStatusCode.Unknown;

  // ⌘K: this app's actions while its page is open, under its name. Stop and
  // Start have their own ids: the registry re-registers on id changes only.
  // Phase 2 adds Back up now.
  // The control actions are disabled while one runs, like the page's buttons.
  const busy = pending !== null;
  const paletteActions: PaletteAction[] = app
    ? [
        { id: "app.restart", label: "Restart", icon: RotateCw, disabled: busy, run: () => void runControl("restart") },
        canStop
          ? { id: "app.stop", label: "Stop", icon: Square, disabled: busy, run: () => void runControl("stop") }
          : { id: "app.start", label: "Start", icon: Play, disabled: busy, run: () => void runControl("start") },
        {
          id: "app.update",
          label: "Pull latest images",
          icon: ArrowUpCircle,
          disabled: busy,
          keywords: ["update", "upgrade", "images"],
          run: () => void runControl("update"),
        },
        { id: "app.logs", label: "View logs", icon: ScrollText, run: () => setTab("logs") },
      ]
    : [];
  usePaletteActions(appLabel(app?.name, appId ?? ""), paletteActions);

  if (!app) {
    if (loading) return null;
    return (
      <NotFound
        title="App not found"
        description="This app doesn't exist on this server."
      />
    );
  }

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
                  to={templatePath(inst.template_id)}
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
          current={ownership?.current ?? null}
          target={upgradeTarget}
          onClose={() => setUpgrade(null)}
          onDone={reloadManaged}
        />
      ) : null}

      <StaleVarsBadge key={app.id} appId={app.id} />

      <AppMetricBlocks serverId={app.serverId} appId={app.id} />

      <UnderlineTabs<Tab>
        value={tab}
        onValueChange={setTab}
        label="App sections"
        items={[
          { value: "logs", label: "Logs" },
          { value: "metrics", label: "Metrics" },
          { value: "configuration", label: "Configuration" },
          {
            value: "backups",
            label: (
              <>
                Backups
                {needsAttention(backupsByApp?.[app.id]) ? (
                  <TriangleAlert
                    role="img"
                    aria-label="A backup is late or failing"
                    className="ml-1.5 inline size-3.5 align-text-bottom text-status-warn"
                  />
                ) : null}
              </>
            ),
          },
          { value: "settings", label: "Settings" },
        ]}
      >
        <TabsContent value="logs" className="mt-6">
          <AppLogsTab appId={app.id} />
        </TabsContent>
        <TabsContent value="metrics" className="mt-6">
          <AppMetricsTab serverId={app.serverId} appId={app.id} appName={appLabel(app.name, app.id)} />
        </TabsContent>
        <TabsContent value="configuration" className="mt-0">
          <ConfigurationTab
            appId={app.id}
            ownership={ownership}
            onSaved={reloadManaged}
            headerActions={
              <Button variant="outline" onClick={() => setHistoryOpen(true)}>
                <History className="size-4" aria-hidden /> History
              </Button>
            }
          />
        </TabsContent>
        <TabsContent value="backups" className="mt-6">
          <AppBackups appId={app.id} />
        </TabsContent>
        <TabsContent value="settings" className="mt-6">
          <AppSettingsTab
            appId={app.id}
            name={app.name}
            ownership={ownership}
            onUnlinked={reloadManaged}
            onRename={rename}
            onDelete={() => setDeleteOpen(true)}
            onSaveAsTemplate={() => void saveAsTemplate.run()}
            savingTemplate={saveAsTemplate.busy}
          />
        </TabsContent>
      </UnderlineTabs>

      <DeleteAppDialog name={appLabel(app.name, app.id)} open={deleteOpen} onOpenChange={setDeleteOpen} onConfirm={deleteApp} />
      <HistorySheet key={app.id} appId={app.id} ownership={ownership} open={historyOpen} onOpenChange={setHistoryOpen} />
    </div>
  );
}

const CONTROL_DONE: Record<ControlAction, string> = {
  start: "App started",
  stop: "App stopped",
  restart: "App restarted",
  update: "App updated",
};

// ConfigurationTab is the app's editor, or for an app a template manages
// its instance's parameters (never the plain editor, whose save would be
// refused). headerActions (the History button) go in either panel's bar.
function ConfigurationTab({
  appId,
  ownership,
  onSaved,
  headerActions,
}: {
  appId: string;
  ownership: Ownership;
  onSaved: () => void;
  headerActions?: ReactNode;
}) {
  if (ownership?.error) return <LoadError message={ownership.error} onRetry={() => void ownership.reload()} />;
  if (ownership?.loading) return <PageSpinner />;
  if (ownership?.instance && ownership.current) {
    const inst = ownership.instance;
    return (
      <InstancePanel
        key={`${inst.revision}-${inst.updated_at}`}
        instance={inst}
        current={ownership.current}
        onSaved={onSaved}
        headerActions={headerActions}
      />
    );
  }
  return <AppEditorPanel appId={appId} headerActions={headerActions} />;
}
