import { useCallback, useEffect, useMemo, useState } from "react";
import { useNavigate, useParams, useSearchParams } from "react-router-dom";
import {
  ArrowUpCircle,
  Play,
  RotateCw,
  Square,
  SquarePen,
  Trash2,
} from "lucide-react";
import { toast } from "sonner";

import { useAppBreadcrumbs } from "@/layouts/use-app-layout";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { ScrollArea } from "@/components/ui/scroll-area";
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
import { AppEditorPanel } from "@/components/app-editor-panel";
import { AppStatusBadge } from "@/components/app-status-badge";
import { useApps } from "@/context/use-apps";
import { useNotifications } from "@/context/use-notifications";
import { useServers } from "@/context/use-servers";
import { apiBaseUrl } from "@/config";
import type { ControlAction } from "@/context/apps-context-base";

const base = apiBaseUrl.endsWith("/") ? apiBaseUrl.slice(0, -1) : apiBaseUrl;

type LogEntry = {
  timestamp: number;
  level: number;
  message: string;
  container?: string;
};

const LEVEL_CLASS: Record<number, string> = {
  4: "text-yellow-500",
  5: "text-red-500",
  6: "text-red-600 font-semibold",
};

const TABS = ["logs", "editor", "settings"] as const;

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
          <TabsTrigger value="settings">Settings</TabsTrigger>
        </TabsList>
        <TabsContent value="logs">
          <LogsTab appId={app.id} />
        </TabsContent>
        <TabsContent value="editor">
          <AppEditorPanel appId={app.id} />
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
  const [logs, setLogs] = useState<LogEntry[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const fetchLogs = useCallback(async () => {
    if (!activeServerId) return;
    setLoading(true);
    setError(null);
    try {
      const url = `${base}/api/v1/app/get-logs?server_id=${encodeURIComponent(
        activeServerId,
      )}&app_id=${encodeURIComponent(appId)}&tail=200`;
      const res = await fetch(url, { credentials: "include" });
      if (!res.ok) throw new Error(`Request failed: ${res.status}`);
      const accepted = (await res.json()) as { data?: { request_id?: string } };
      const ref = accepted.data?.request_id;
      if (!ref) throw new Error("No request id");
      const result = await waitFor(ref);
      if (result.status && result.status !== 0) {
        throw new Error(result.error || "Failed to fetch logs");
      }
      const payload = result.payload as { logs?: LogEntry[] } | undefined;
      setLogs(payload?.logs ?? []);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Failed to fetch logs");
    } finally {
      setLoading(false);
    }
  }, [appId, activeServerId, waitFor]);

  useEffect(() => {
    void fetchLogs();
  }, [fetchLogs]);

  return (
    <Card>
      <CardHeader className="flex-row items-center justify-between space-y-0">
        <CardTitle>Logs</CardTitle>
        <Button
          size="sm"
          variant="outline"
          onClick={() => void fetchLogs()}
          disabled={loading}
        >
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
        ) : logs.length === 0 ? (
          <p className="text-sm text-muted-foreground">No logs.</p>
        ) : (
          <ScrollArea className="h-96 rounded-md border bg-muted/30">
            <div className="p-3 font-mono text-xs">
              {logs.map((l, i) => (
                <div key={i} className="whitespace-pre-wrap break-all">
                  {l.container ? (
                    <span className="text-muted-foreground">{l.container} </span>
                  ) : null}
                  <span className={LEVEL_CLASS[l.level] ?? ""}>{l.message}</span>
                </div>
              ))}
            </div>
          </ScrollArea>
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
                <AlertDialogAction onClick={() => void del()}>
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
