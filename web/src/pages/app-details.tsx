import { useCallback, useEffect, useMemo, useState } from "react";
import { useNavigate, useParams, useSearchParams } from "react-router-dom";
import {
  ArrowUpCircle,
  History,
  Play,
  RotateCw,
  Square,
  SquarePen,
  Trash2,
} from "lucide-react";
import { toast } from "sonner";

import { useAppBreadcrumbs } from "@/layouts/use-app-layout";
import { Button } from "@/components/ui/button";
import { buttonVariants } from "@/components/ui/button-variants";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Badge } from "@/components/ui/badge";
import { Spinner } from "@/components/ui/spinner";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
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
import { AppStatusBadge } from "@/components/app-status-badge";
import { useApps } from "@/context/use-apps";
import { useNotifications } from "@/context/use-notifications";
import { useServers } from "@/context/use-servers";
import { apiBaseUrl } from "@/config";
import type { AppRevisions, ControlAction } from "@/context/apps-context-base";

const base = apiBaseUrl.endsWith("/") ? apiBaseUrl.slice(0, -1) : apiBaseUrl;

const TABS = ["logs", "editor", "history", "settings"] as const;

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

  const [busy, setBusy] = useState(false);

  const runControl = async (action: ControlAction) => {
    if (!appId) return;
    setBusy(true);
    try {
      await control(appId, action);
      toast.success(`${action} succeeded`);
    } catch (e) {
      toast.error(`${action} failed`, {
        description: e instanceof Error ? e.message : undefined,
      });
    } finally {
      setBusy(false);
    }
  };

  if (!app) {
    return (
      <div className="flex min-h-60 items-center justify-center">
        <p className="text-sm text-muted-foreground">App not found.</p>
      </div>
    );
  }

  return (
    <div className="space-y-6">
      {/* Header: icon + name/description/status + controls */}
      <div className="flex flex-col gap-6 md:flex-row md:items-start">
        <AppIcon
          name={app.name}
          icon={app.icon}
          color={app.color}
          className="size-28 shrink-0 rounded-xl shadow-md"
        />
        <div className="min-w-0 flex-1">
          <h1 className="text-2xl font-bold">{app.name}</h1>
          <div className="mt-2 flex items-center gap-3">
            <AppStatusBadge status={statusByApp[app.id]} />
            {app.version ? (
              <span className="text-sm text-muted-foreground">
                v{app.version}
              </span>
            ) : null}
          </div>
          <div className="mt-4 flex flex-wrap gap-2">
            <Button
              variant="outline"
              size="sm"
              disabled={busy}
              onClick={() => void runControl("start")}
            >
              <Play className="size-4" /> Start
            </Button>
            <Button
              variant="outline"
              size="sm"
              disabled={busy}
              onClick={() => void runControl("stop")}
            >
              <Square className="size-4" /> Stop
            </Button>
            <Button
              variant="outline"
              size="sm"
              disabled={busy}
              onClick={() => void runControl("restart")}
            >
              <RotateCw className="size-4" /> Restart
            </Button>
            <Button
              variant="outline"
              size="sm"
              disabled={busy}
              onClick={() => void runControl("update")}
            >
              <ArrowUpCircle className="size-4" /> Update
            </Button>
            <Button
              variant="outline"
              size="sm"
              onClick={() => setTab("editor")}
            >
              <SquarePen className="size-4" /> Edit
            </Button>
          </div>
        </div>
      </div>

      <Tabs value={tab} onValueChange={setTab}>
        <TabsList>
          <TabsTrigger value="logs">Logs</TabsTrigger>
          <TabsTrigger value="editor">Editor</TabsTrigger>
          <TabsTrigger value="history">History</TabsTrigger>
          <TabsTrigger value="settings">Settings</TabsTrigger>
        </TabsList>
        <TabsContent value="logs">
          <LogsTab appId={app.id} />
        </TabsContent>
        <TabsContent value="editor">
          <AppEditorPanel appId={app.id} />
        </TabsContent>
        <TabsContent value="history">
          <HistoryTab appId={app.id} />
        </TabsContent>
        <TabsContent value="settings">
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
      toast.error("Rollback failed", {
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

  return (
    <Card>
      <CardHeader className="flex-row items-center justify-between space-y-0">
        <CardTitle>History</CardTitle>
        <Button size="sm" variant="outline" onClick={() => void load()} disabled={loading}>
          Refresh
        </Button>
      </CardHeader>
      <CardContent>
        {loading ? (
          <div className="flex h-40 items-center justify-center">
            <Spinner />
          </div>
        ) : error ? (
          <p className="text-sm text-destructive">{error}</p>
        ) : !data || data.revisions.length === 0 ? (
          <p className="text-sm text-muted-foreground">No history yet.</p>
        ) : (
          <div className="divide-y">
            {data.revisions.map((rev) => {
              const isCurrent = rev.hash === data.current;
              const isDeployed = data.deployed !== "" && rev.hash === data.deployed;
              const isUndeployedDraft = isCurrent && data.deployed !== "" && !isDeployed;
              return (
                <div key={rev.hash} className="flex items-center gap-3 py-2.5">
                  <code className="shrink-0 rounded bg-muted px-1.5 py-0.5 font-mono text-xs">
                    {rev.hash.slice(0, 8)}
                  </code>
                  <div className="min-w-0 flex-1">
                    <div className="truncate text-sm">{rev.subject}</div>
                    <div className="text-xs text-muted-foreground">
                      {new Date(rev.timestamp * 1000).toLocaleString()}
                    </div>
                  </div>
                  {isDeployed ? (
                    <Badge className="shrink-0">Deployed</Badge>
                  ) : null}
                  {isCurrent ? (
                    isUndeployedDraft ? (
                      <>
                        <Badge variant="outline" className="shrink-0">
                          Draft
                        </Badge>
                        <Button size="sm" onClick={() => void doDeploy()} disabled={busy}>
                          Deploy
                        </Button>
                      </>
                    ) : (
                      <Badge variant="secondary" className="shrink-0">
                        Current
                      </Badge>
                    )
                  ) : (
                    <AlertDialog>
                      <AlertDialogTrigger asChild>
                        <Button size="sm" variant="outline" disabled={busy}>
                          <History className="size-4" /> Rollback
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
                            Rollback
                          </AlertDialogAction>
                        </AlertDialogFooter>
                      </AlertDialogContent>
                    </AlertDialog>
                  )}
                </div>
              );
            })}
          </div>
        )}
      </CardContent>
    </Card>
  );
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
    <div className="space-y-6">
      <Card>
        <CardHeader>
          <CardTitle>Rename</CardTitle>
        </CardHeader>
        <CardContent>
          <div className="flex items-end gap-2">
            <div className="grid flex-1 gap-1.5">
              <Label htmlFor="app-name">Name</Label>
              <Input
                id="app-name"
                value={newName}
                onChange={(e) => setNewName(e.target.value)}
              />
            </div>
            <Button
              onClick={() => void save()}
              disabled={busy || !newName.trim() || newName === name}
            >
              Save
            </Button>
          </div>
        </CardContent>
      </Card>

      <Card className="border-destructive/40">
        <CardHeader>
          <CardTitle className="text-destructive">Danger zone</CardTitle>
        </CardHeader>
        <CardContent>
          <AlertDialog>
            <AlertDialogTrigger asChild>
              <Button variant="destructive" disabled={busy}>
                <Trash2 className="size-4" /> Delete app
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
        </CardContent>
      </Card>
    </div>
  );
}
