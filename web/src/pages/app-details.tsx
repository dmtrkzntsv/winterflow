import { useMemo, useRef, useState } from "react";
import { useParams, type Location } from "react-router-dom";
import { ArrowUpCircle, DatabaseBackup, Play, RotateCw, ScrollText, Square, TriangleAlert } from "lucide-react";
import { toast } from "sonner";

import { TabsContent } from "@/components/ui/tabs";
import { FiringBanner } from "@/components/alerts/firing-banner";
import { AppBackups } from "@/components/app-backups";
import { AppEditorPanel } from "@/components/app-editor-panel";
import { AppMetricsTab } from "@/components/app-metrics";
import { AppHeader } from "@/components/app-page/app-header";
import { AppLogsTab } from "@/components/app-page/app-logs-tab";
import { AppOverview } from "@/components/app-page/app-overview";
import { DeployFailedBanner } from "@/components/app-page/deploy-failed-banner";
import { AppSettingsTab } from "@/components/app-page/app-settings-tab";
import { HistorySheet } from "@/components/app-page/history-sheet";
import type { Ownership } from "@/components/app-page/ownership";
import { DeleteAppDialog } from "@/components/delete-app-dialog";
import { InstancePanel } from "@/components/instance-panel";
import { NotFound } from "@/components/not-found";
import { LoadError, PageSpinner } from "@/components/page-states";
import { StaleVarsBadge } from "@/components/stale-vars-badge";
import { UnderlineTabs } from "@/components/underline-tabs";
import { UnsavedChangesGuard } from "@/components/unsaved-changes-guard";
import { UpgradeInstanceDialog } from "@/components/upgrade-instance-dialog";
import { AppStatusCode, type ControlAction } from "@/context/apps-context-base";
import type { PaletteAction } from "@/context/palette-context-base";
import { useApps } from "@/context/use-apps";
import { usePaletteActions } from "@/context/use-palette";
import { useServers } from "@/context/use-servers";
import { useDeleteApp, useSaveAsTemplate } from "@/hooks/use-app-actions";
import { useAppDetail } from "@/hooks/use-app-detail";
import { useAppInstance } from "@/hooks/use-app-instance";
import { useHistorySheet } from "@/hooks/use-history-sheet";
import { useAddressRegistrations } from "@/hooks/use-registrations";
import { useBackUpNow } from "@/hooks/use-run-backup";
import { useTabParam } from "@/hooks/use-tab-param";
import { useTemplatesApi } from "@/hooks/use-templates-api";
import { appPublishedPorts } from "@/hooks/use-used-host-ports";
import { usePageTitle } from "@/layouts/use-page-title";
import { hasRunningContainers } from "@/lib/app-status";
import { portAddresses } from "@/lib/app-summary";
import { hasActiveBackup, needsAttention, runnableBackups } from "@/lib/backups";
import { appLabel } from "@/lib/kpi";
import { LEGACY_APP_TABS, type AppTab } from "@/lib/routes";
import type { TemplateView } from "@/lib/templates";

const TABS = ["overview", "logs", "configuration", "backups", "metrics", "settings"] as const satisfies readonly AppTab[];
type Tab = (typeof TABS)[number];

// staysOnConfiguration: a navigation that keeps this app's Configuration
// tab open (the History sheet's ?history=1) drops no edits.
function staysOnConfiguration(next: Location, current: Location): boolean {
  if (next.pathname !== current.pathname) return false;
  const raw = new URLSearchParams(next.search).get("tab") ?? "";
  const aliases: Readonly<Record<string, string>> = LEGACY_APP_TABS;
  return (Object.hasOwn(aliases, raw) ? aliases[raw] : raw) === "configuration";
}

const CONTROL_DONE: Record<ControlAction, string> = {
  start: "App started",
  stop: "App stopped",
  restart: "App restarted",
  update: "Latest images pulled",
};

const CONTROL_FAILED: Record<ControlAction, string> = {
  start: "Couldn't start the app",
  stop: "Couldn't stop the app",
  restart: "Couldn't restart the app",
  update: "Couldn't pull the latest images",
};

// What the header says while a control action runs.
const CONTROL_PROGRESS: Record<ControlAction, string> = {
  start: "Starting…",
  stop: "Stopping…",
  restart: "Restarting…",
  update: "Pulling images…",
};

// AppDetailsPage is /server/:id/app/:appId: the header, the banners that
// matter now, and the tabs (Overview first). History is a side sheet
// (?history=1). The page stays mounted from app to app, so every per-app
// part is keyed by the app's id.
export default function AppDetailsPage() {
  const { appId = "" } = useParams();
  const { apps, statusByApp, backupsByApp, deployFailures, loading, control, refresh } = useApps();
  const templatesApi = useTemplatesApi();
  const [tab, setTab] = useTabParam<Tab>(TABS, "overview", { aliases: LEGACY_APP_TABS });
  // After useTabParam: see useHistorySheet.
  const [historyOpen, setHistoryOpen] = useHistorySheet();

  const app = useMemo(() => apps.find((a) => a.id === appId), [apps, appId]);
  const { servers } = useServers();
  // The page reads the app once per version, and again each time the
  // Overview is shown (its backups); its compose files give the published
  // ports the header and Access open.
  const [visit, setVisit] = useState({ tab, n: 0 });
  if (visit.tab !== tab) setVisit({ tab, n: tab === "overview" ? visit.n + 1 : visit.n });
  const read = useAppDetail(app?.id ?? "", app?.version ?? "", visit.n);
  const serverIp = servers.find((s) => s.id === app?.serverId)?.capabilities?.server_ip;
  const detail = read.appId === app?.id ? read.detail : null;
  const ports = useMemo(() => (detail ? portAddresses(appPublishedPorts(detail), serverIp) : []), [detail, serverIp]);
  // One read of the server's domains for the header's and Overview's HTTPS state.
  const registrations = useAddressRegistrations(app?.serverId);
  const lookup = useAppInstance(app);
  // ownership is the template side of the app (null for a plain app). Only
  // this app's own instance is ever used.
  const ownership: Ownership = app?.templateId
    ? { ...lookup, instance: lookup.instance?.app_id === app.id ? lookup.instance : null }
    : null;
  const inst = ownership?.instance ?? null;
  const latest = ownership?.current?.latest_revision ?? 0;
  const behind = inst !== null && latest > inst.revision;
  // historyChanges counts the roll backs and draft deploys done in History;
  // Configuration reads the app again on each (see AppEditorPanel).
  const [historyChanges, setHistoryChanges] = useState(0);
  // configDirty: the Configuration editor holds unsaved edits; leaving the
  // tab or the page asks first (UnsavedChangesGuard).
  const [configDirty, setConfigDirty] = useState(false);
  const [upgrade, setUpgrade] = useState<{ appId: string; target: TemplateView } | null>(null);
  const upgradeTarget = upgrade && upgrade.appId === app?.id ? upgrade.target : null;
  const reloadManaged = () => void Promise.all([lookup.reload(), refresh()]);
  // openUpgrade reads the template's latest version and opens the update
  // dialog (the header's "Update available" and the Configuration tab's
  // "A newer version is available").
  const openUpgrade = async () => {
    if (!inst) return;
    try {
      setUpgrade({ appId: inst.app_id, target: await templatesApi.getTemplate(inst.template_id) });
    } catch (e) {
      toast.error("Couldn't load the template", { description: e instanceof Error ? e.message : undefined });
    }
  };

  // The running control actions, each with its app: the page stays mounted
  // from app to app, so another app's page must not show them.
  const [controls, setControls] = useState<{ appId: string; action: ControlAction }[]>([]);
  const pending = controls.find((c) => c.appId === appId)?.action ?? null;
  const [deleteOpen, setDeleteOpen] = useState(false);
  // deletedApp: the app deleted from this page; leaving it then asks
  // nothing (its unsaved edits went with it).
  const deletedApp = useRef<string | null>(null);
  const deleteApp = useDeleteApp(appId, () => {
    deletedApp.current = appId;
  });
  const saveAsTemplate = useSaveAsTemplate(appId, app?.name ?? "");
  const backUpNow = useBackUpNow(appId);
  const title = app ? appLabel(app.name) : "App";
  usePageTitle(title);

  const runControl = async (action: ControlAction) => {
    if (!appId) return;
    const entry = { appId, action };
    // The toast outlives the page's app: it names the app it is about.
    const label = title;
    setControls((cur) => [...cur, entry]);
    try {
      await control(appId, action);
      toast.success(CONTROL_DONE[action], { description: label });
      // An update pulls a git-sourced app's source too: Configuration reads
      // the app again (or says it changed while it holds edits).
      if (action === "update") setHistoryChanges((n) => n + 1);
    } catch (e) {
      toast.error(CONTROL_FAILED[action], {
        description: [label, e instanceof Error ? e.message : ""].filter(Boolean).join(": "),
      });
    } finally {
      setControls((cur) => cur.filter((c) => c !== entry));
    }
  };

  // Whether the app may have backup copies to delete with it: a backup set
  // up with a storage, or one the agent reports. Unknown (no read yet, or
  // its backups unreadable) counts as maybe.
  const mayHaveBackups =
    !detail ||
    !!detail.backups_error ||
    (detail.backups ?? []).some((b) => !!b.storage) ||
    (backupsByApp?.[appId]?.length ?? 0) > 0;
  const status = statusByApp[appId];
  // failure is why the app's deploy failed: a create's in this session, or
  // a pending template instance's (its deploy never succeeded). The banner
  // stays while the instance is pending; the pill reads Failed for it only
  // while no report shows the app running (the status report wins).
  const instanceFailure = (inst?.state === "pending" ? inst.last_error : "") || "";
  const failure = deployFailures?.[appId] || instanceFailure;
  const failed = !!deployFailures?.[appId] || (instanceFailure !== "" && status !== AppStatusCode.Active);
  const running = hasRunningContainers(status);
  // The control actions are disabled while one runs, like the header's.
  const busy = pending !== null;
  // ⌘K: this app's actions while its page is open, under its name. Stop and
  // Start have their own ids: the registry re-registers on id changes only.
  const paletteActions: PaletteAction[] = app
    ? [
        { id: "app.restart", label: "Restart", icon: RotateCw, disabled: busy, run: () => void runControl("restart") },
        running
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
        // Only for an app with a backup Back up now runs (on, with a
        // storage, not custom; until the app is read, one the agent reports
        // on): elsewhere it would only say there is nothing to back up.
        ...((detail ? runnableBackups(detail.backups ?? []).length > 0 : hasActiveBackup(backupsByApp?.[app.id]))
          ? [
              {
                id: "app.backup",
                label: "Back up now",
                icon: DatabaseBackup,
                keywords: ["backup", "copy"],
                disabled: backUpNow.busy,
                run: () => void backUpNow.run(),
              },
            ]
          : []),
      ]
    : [];
  usePaletteActions(title, paletteActions);

  if (!app) {
    if (loading) return null;
    return <NotFound title="App not found" description="This app doesn't exist on this server." />;
  }

  const name = appLabel(app.name);
  const progress = pending
    ? CONTROL_PROGRESS[pending]
    : backUpNow.restoring
      ? "Restoring…"
      : backUpNow.busy
        ? "Backing up…"
        : null;

  return (
    // A flex column so the Logs tab can fill what is left of the viewport.
    <div className="flex flex-1 flex-col gap-6">
      <AppHeader
        app={app}
        registrations={registrations}
        ports={ports}
        status={status}
        failed={failed}
        instance={inst}
        behind={behind}
        pending={pending}
        onControl={(action) => void runControl(action)}
        onUpdateTemplate={() => void openUpgrade()}
        backUpBusy={backUpNow.busy}
        progress={progress}
        onBackUpNow={() => void backUpNow.run()}
        canSaveAsTemplate={ownership === null}
        savingTemplate={saveAsTemplate.busy}
        onSaveAsTemplate={() => void saveAsTemplate.run()}
        onDelete={() => setDeleteOpen(true)}
      />

      {failure ? (
        <DeployFailedBanner
          key={app.id}
          error={failure}
          onOpenConfiguration={tab === "configuration" ? undefined : () => setTab("configuration")}
        />
      ) : null}
      <StaleVarsBadge key={app.id} appId={app.id} />
      <FiringBanner key={`alerts-${app.id}`} serverId={app.serverId} appId={app.id} />

      {upgradeTarget && inst ? (
        <UpgradeInstanceDialog
          // The page's live status says whether the app is stopped now.
          instance={{ ...inst, status_code: status ?? inst.status_code }}
          appName={name}
          current={ownership?.current ?? null}
          target={upgradeTarget}
          onClose={() => setUpgrade(null)}
          onDone={reloadManaged}
        />
      ) : null}

      <UnsavedChangesGuard
        when={tab === "configuration" && configDirty}
        stays={(next, current) => deletedApp.current === appId || staysOnConfiguration(next, current)}
        description="Your changes to this app's configuration haven't been saved. Leaving drops them."
      />

      <UnderlineTabs<Tab>
        className="flex grow basis-0 flex-col"
        value={tab}
        onValueChange={setTab}
        label="App sections"
        items={[
          { value: "overview", label: "Overview" },
          { value: "logs", label: "Logs" },
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
          { value: "metrics", label: "Metrics" },
          { value: "settings", label: "Settings" },
        ]}
      >
        <TabsContent value="overview" className="mt-6">
          <AppOverview
            key={app.id}
            app={app}
            status={status}
            read={read}
            ports={ports}
            registrations={registrations}
            onOpenHistory={() => setHistoryOpen(true)}
          />
        </TabsContent>
        <TabsContent value="logs" className="mt-6 flex grow basis-0 flex-col">
          <AppLogsTab key={app.id} appId={app.id} />
        </TabsContent>
        <TabsContent value="configuration" className="mt-0">
          <ConfigurationTab
            key={app.id}
            appId={app.id}
            ownership={ownership}
            onSaved={reloadManaged}
            onOpenHistory={() => setHistoryOpen(true)}
            historyChanges={historyChanges}
            onDirtyChange={setConfigDirty}
            onUpgrade={() => void openUpgrade()}
          />
        </TabsContent>
        <TabsContent value="backups" className="mt-6">
          <AppBackups key={app.id} appId={app.id} />
        </TabsContent>
        <TabsContent value="metrics" className="mt-6">
          <AppMetricsTab key={app.id} serverId={app.serverId} appId={app.id} appName={name} />
        </TabsContent>
        <TabsContent value="settings" className="mt-6">
          <AppSettingsTab
            key={app.id}
            app={app}
            ownership={ownership}
            running={running}
            onDetached={reloadManaged}
            onSaveAsTemplate={() => void saveAsTemplate.run()}
            savingTemplate={saveAsTemplate.busy}
            onDelete={() => setDeleteOpen(true)}
          />
        </TabsContent>
      </UnderlineTabs>

      <HistorySheet
        key={app.id}
        appId={app.id}
        ownership={ownership}
        open={historyOpen}
        onOpenChange={setHistoryOpen}
        onChanged={() => setHistoryChanges((n) => n + 1)}
      />
      <DeleteAppDialog name={name} open={deleteOpen} hasBackups={mayHaveBackups} onOpenChange={setDeleteOpen} onConfirm={deleteApp} />
    </div>
  );
}

// ConfigurationTab is the app's editor, or for an app a template manages
// its instance's parameters (never the plain editor, whose save would be
// refused). onOpenHistory puts a History button in either panel's bar;
// onUpgrade is the instance panel's "A newer version is available".
function ConfigurationTab({
  appId,
  ownership,
  onSaved,
  onOpenHistory,
  historyChanges,
  onDirtyChange,
  onUpgrade,
}: {
  appId: string;
  ownership: Ownership;
  onSaved: () => void;
  onOpenHistory?: () => void;
  // historyChanges counts the roll backs and draft deploys done in History:
  // the editor reads the app again, or says so when it holds edits.
  historyChanges: number;
  onDirtyChange: (dirty: boolean) => void;
  // onUpgrade opens the page's update dialog (a template-managed app's
  // "A newer version is available").
  onUpgrade: () => void;
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
        onOpenHistory={onOpenHistory}
        latestRevision={ownership.current.latest_revision}
        onUpgrade={onUpgrade}
        onDirtyChange={onDirtyChange}
      />
    );
  }
  return (
    <AppEditorPanel appId={appId} onOpenHistory={onOpenHistory} reloadSignal={historyChanges} onDirtyChange={onDirtyChange} />
  );
}
