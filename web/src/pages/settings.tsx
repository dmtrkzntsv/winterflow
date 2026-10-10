import { Fragment, useCallback, useEffect, useMemo, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { Plus, RefreshCw, Trash2 } from "lucide-react";
import { toast } from "sonner";

import { useAppBreadcrumbs } from "@/layouts/use-app-layout";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { buttonVariants } from "@/components/ui/button-variants";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Spinner } from "@/components/ui/spinner";
import {
  useDocker,
  type Network,
  type Registry,
  type Volume,
} from "@/context/use-docker";
import { useApps } from "@/context/use-apps";
import { useProfile } from "@/context/use-profile";
import { cn } from "@/lib/utils";

const TABS = ["registries", "networks", "volumes", "agent"] as const;

// SettingsPage is the server-level settings view (v1 "Server Settings"):
// Docker registries and networks, plus agent self-update — one tab per
// section, synced to ?tab= like the app details page.
export default function SettingsPage() {
  const breadcrumbs = useMemo(
    () => [{ label: "Server" }, { label: "Settings" }],
    [],
  );
  useAppBreadcrumbs(breadcrumbs);

  const docker = useDocker();
  const [searchParams, setSearchParams] = useSearchParams();

  const tabParam = searchParams.get("tab") ?? "registries";
  const tab = (TABS as readonly string[]).includes(tabParam)
    ? tabParam
    : "registries";
  const setTab = (next: string) => {
    setSearchParams(next === "registries" ? {} : { tab: next }, {
      replace: true,
    });
  };

  if (!docker.hasServer) {
    return (
      <p className="text-sm text-muted-foreground">
        Select a server to manage its settings.
      </p>
    );
  }

  return (
    <Tabs value={tab} onValueChange={setTab}>
      <TabsList>
        <TabsTrigger value="registries">Registries</TabsTrigger>
        <TabsTrigger value="networks">Networks</TabsTrigger>
        <TabsTrigger value="volumes">Volumes</TabsTrigger>
        <TabsTrigger value="agent">Agent</TabsTrigger>
      </TabsList>
      <TabsContent value="registries">
        <RegistriesCard docker={docker} />
      </TabsContent>
      <TabsContent value="networks">
        <NetworksCard docker={docker} />
      </TabsContent>
      <TabsContent value="volumes">
        <VolumesCard docker={docker} />
      </TabsContent>
      <TabsContent value="agent">
        <AgentCard docker={docker} />
      </TabsContent>
    </Tabs>
  );
}

function AgentCard({ docker }: { docker: Docker }) {
  const [version, setVersion] = useState("");
  const [busy, setBusy] = useState(false);

  const update = async () => {
    setBusy(true);
    try {
      await docker.updateAgent(version.trim());
      toast.success("Agent update dispatched", {
        description: "The agent will restart on the new version.",
      });
      setVersion("");
    } catch (e) {
      toast.error("Failed to update agent", {
        description: e instanceof Error ? e.message : undefined,
      });
    } finally {
      setBusy(false);
    }
  };

  return (
    <Card>
      <CardHeader>
        <CardTitle>Agent</CardTitle>
      </CardHeader>
      <CardContent>
        <div className="flex items-end gap-2">
          <div className="grid flex-1 gap-1.5">
            <Label htmlFor="agent-version">Update to version</Label>
            <Input
              id="agent-version"
              placeholder="e.g. 1.2.3"
              value={version}
              onChange={(e) => setVersion(e.target.value)}
            />
          </div>
          <Button onClick={() => void update()} disabled={busy || !version.trim()}>
            {busy ? "Updating…" : "Update agent"}
          </Button>
        </div>
        <p className="mt-2 text-xs text-muted-foreground">
          The agent downloads the release, replaces its binary, and restarts.
          Only newer versions are applied.
        </p>
      </CardContent>
    </Card>
  );
}

type Docker = ReturnType<typeof useDocker>;

function RegistriesCard({ docker }: { docker: Docker }) {
  const [items, setItems] = useState<Registry[]>([]);
  const [loading, setLoading] = useState(true);
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [form, setForm] = useState({ address: "", username: "", password: "" });

  const load = useCallback(async () => {
    setLoading(true);
    try {
      setItems(await docker.listRegistries());
    } catch (e) {
      toast.error("Failed to load registries", {
        description: e instanceof Error ? e.message : undefined,
      });
    } finally {
      setLoading(false);
    }
  }, [docker]);

  useEffect(() => {
    void load();
  }, [load]);

  const add = async () => {
    setBusy(true);
    try {
      await docker.createRegistry(form.address, form.username, form.password);
      toast.success("Registry added");
      setOpen(false);
      setForm({ address: "", username: "", password: "" });
      await load();
    } catch (e) {
      toast.error("Failed to add registry", {
        description: e instanceof Error ? e.message : undefined,
      });
    } finally {
      setBusy(false);
    }
  };

  const remove = async (address: string) => {
    try {
      await docker.deleteRegistry(address);
      toast.success("Registry removed");
      await load();
    } catch (e) {
      toast.error("Failed to remove registry", {
        description: e instanceof Error ? e.message : undefined,
      });
    }
  };

  return (
    <Card>
      <CardHeader className="flex-row items-center justify-between space-y-0">
        <CardTitle>Registries</CardTitle>
        <Dialog open={open} onOpenChange={setOpen}>
          <DialogTrigger asChild>
            <Button size="sm">
              <Plus className="size-4" /> Add registry
            </Button>
          </DialogTrigger>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>Add registry</DialogTitle>
            </DialogHeader>
            <div className="grid gap-3">
              <div className="grid gap-1.5">
                <Label htmlFor="reg-addr">Address</Label>
                <Input
                  id="reg-addr"
                  placeholder="registry.example.com"
                  value={form.address}
                  onChange={(e) =>
                    setForm((f) => ({ ...f, address: e.target.value }))
                  }
                />
              </div>
              <div className="grid gap-1.5">
                <Label htmlFor="reg-user">Username</Label>
                <Input
                  id="reg-user"
                  value={form.username}
                  onChange={(e) =>
                    setForm((f) => ({ ...f, username: e.target.value }))
                  }
                />
              </div>
              <div className="grid gap-1.5">
                <Label htmlFor="reg-pass">Password</Label>
                <Input
                  id="reg-pass"
                  type="password"
                  value={form.password}
                  onChange={(e) =>
                    setForm((f) => ({ ...f, password: e.target.value }))
                  }
                />
              </div>
            </div>
            <DialogFooter>
              <Button variant="outline" onClick={() => setOpen(false)}>
                Cancel
              </Button>
              <Button
                onClick={() => void add()}
                disabled={busy || !form.address || !form.username}
              >
                {busy ? "Adding…" : "Add"}
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
      </CardHeader>
      <CardContent>
        {loading ? (
          <Spinner />
        ) : items.length === 0 ? (
          <p className="text-sm text-muted-foreground">No registries.</p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Address</TableHead>
                <TableHead className="w-12" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.map((r) => (
                <TableRow key={r.address}>
                  <TableCell className="font-mono">{r.address}</TableCell>
                  <TableCell>
                    <Button
                      size="icon"
                      variant="ghost"
                      className="size-8"
                      onClick={() => void remove(r.address)}
                    >
                      <Trash2 className="size-4" />
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </CardContent>
    </Card>
  );
}

function NetworksCard({ docker }: { docker: Docker }) {
  const [items, setItems] = useState<Network[]>([]);
  const [loading, setLoading] = useState(true);
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [name, setName] = useState("");

  const load = useCallback(async () => {
    setLoading(true);
    try {
      setItems(await docker.listNetworks());
    } catch (e) {
      toast.error("Failed to load networks", {
        description: e instanceof Error ? e.message : undefined,
      });
    } finally {
      setLoading(false);
    }
  }, [docker]);

  useEffect(() => {
    void load();
  }, [load]);

  const add = async () => {
    setBusy(true);
    try {
      await docker.createNetwork(name);
      toast.success("Network created");
      setOpen(false);
      setName("");
      await load();
    } catch (e) {
      toast.error("Failed to create network", {
        description: e instanceof Error ? e.message : undefined,
      });
    } finally {
      setBusy(false);
    }
  };

  const remove = async (n: string) => {
    try {
      await docker.deleteNetwork(n);
      toast.success("Network removed");
      await load();
    } catch (e) {
      toast.error("Failed to remove network", {
        description: e instanceof Error ? e.message : undefined,
      });
    }
  };

  // Built-in networks can't be removed; hide the action for them.
  const builtin = new Set(["bridge", "host", "none"]);

  return (
    <Card>
      <CardHeader className="flex-row items-center justify-between space-y-0">
        <CardTitle>Networks</CardTitle>
        <Dialog open={open} onOpenChange={setOpen}>
          <DialogTrigger asChild>
            <Button size="sm">
              <Plus className="size-4" /> Add network
            </Button>
          </DialogTrigger>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>Create network</DialogTitle>
            </DialogHeader>
            <div className="grid gap-1.5">
              <Label htmlFor="net-name">Name</Label>
              <Input
                id="net-name"
                placeholder="my-network"
                value={name}
                onChange={(e) => setName(e.target.value)}
              />
            </div>
            <DialogFooter>
              <Button variant="outline" onClick={() => setOpen(false)}>
                Cancel
              </Button>
              <Button onClick={() => void add()} disabled={busy || !name.trim()}>
                {busy ? "Creating…" : "Create"}
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
      </CardHeader>
      <CardContent>
        {loading ? (
          <Spinner />
        ) : items.length === 0 ? (
          <p className="text-sm text-muted-foreground">No networks.</p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Driver</TableHead>
                <TableHead>Scope</TableHead>
                <TableHead className="w-12" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.map((n) => (
                <TableRow key={n.id ?? n.name}>
                  <TableCell className="font-mono">{n.name}</TableCell>
                  <TableCell>{n.driver ?? "—"}</TableCell>
                  <TableCell>{n.scope ?? "—"}</TableCell>
                  <TableCell>
                    {builtin.has(n.name) ? null : (
                      <Button
                        size="icon"
                        variant="ghost"
                        className="size-8"
                        onClick={() => void remove(n.name)}
                      >
                        <Trash2 className="size-4" />
                      </Button>
                    )}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </CardContent>
    </Card>
  );
}

const VOLUME_NAME_RE = /^[a-zA-Z0-9][a-zA-Z0-9_.-]*$/;

// volumeNameError mirrors the agent's ValidateVolumeName so the dialog can say
// what's wrong before a round-trip; "" means valid.
function volumeNameError(name: string): string {
  if (name.length > 255) return "Use at most 255 characters.";
  if (!VOLUME_NAME_RE.test(name)) {
    return "Start with a letter or digit; use only letters, digits, dots, dashes and underscores.";
  }
  return "";
}

// shortVolumeName drops the compose project prefix ("wf-<id>_data" → "data"):
// the App column already names the owner, so the key is what tells rows
// apart. Callers keep the full name in a title and in confirmations.
function shortVolumeName(v: Volume): string {
  // Anonymous volumes are 64-hex hashes; 12 chars is Docker's own short id.
  if (v.anonymous && /^[0-9a-f]{64}$/.test(v.name)) {
    return `${v.name.slice(0, 12)}…`;
  }
  const prefix = v.app_id && v.project ? `${v.project}_` : "";
  return prefix && v.name.startsWith(prefix)
    ? v.name.slice(prefix.length)
    : v.name;
}

// Same dot + label language as AppStatusIndicator: the normal state stays
// quiet, only Orphaned (data nobody owns any more) draws the eye.
function VolumeStatus({ volume }: { volume: Volume }) {
  const [label, dot, text] = volume.in_use
    ? ["In use", "bg-emerald-500", ""]
    : volume.orphan
      ? ["Orphaned", "bg-amber-400", "text-amber-700 dark:text-amber-400"]
      : ["Unused", "bg-muted-foreground/40", "text-muted-foreground"];
  return (
    <span className={cn("inline-flex items-center gap-1.5 whitespace-nowrap", text)}>
      <span className={cn("size-2 shrink-0 rounded-full", dot)} aria-hidden />
      {label}
    </span>
  );
}

function VolumeOwner({ volume, appName }: { volume: Volume; appName?: string }) {
  if (volume.app_id && appName) {
    return (
      <Link
        to={`/app/${volume.app_id}`}
        className="font-medium underline-offset-4 hover:underline"
      >
        {appName}
      </Link>
    );
  }
  if (volume.orphan) {
    return (
      <span
        className="text-muted-foreground"
        title={`No app ${volume.app_id} on this server`}
      >
        Not found
      </span>
    );
  }
  if (volume.project) {
    return (
      <span className="text-muted-foreground" title="Docker Compose project">
        {volume.project}
      </span>
    );
  }
  return <span className="text-muted-foreground">—</span>;
}

function VolumesCard({ docker }: { docker: Docker }) {
  const { isAdmin } = useProfile();
  const { apps } = useApps();
  const [items, setItems] = useState<Volume[] | null>(null);
  const [loading, setLoading] = useState(true);
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [name, setName] = useState("");
  const [showAnon, setShowAnon] = useState(false);
  const [toDelete, setToDelete] = useState<Volume | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      setItems(await docker.listVolumes());
    } catch (e) {
      toast.error("Failed to load volumes", {
        description: e instanceof Error ? e.message : undefined,
      });
    } finally {
      setLoading(false);
    }
  }, [docker]);

  useEffect(() => {
    void load();
  }, [load]);

  // Single close path: Radix fires onOpenChange only for its own dismissals,
  // so the Cancel button must go through here to reset the name too.
  const changeOpen = (o: boolean) => {
    setOpen(o);
    if (!o) setName("");
  };

  const add = async () => {
    setBusy(true);
    try {
      await docker.createVolume(name.trim());
      toast.success("Volume created");
      changeOpen(false);
      await load();
    } catch (e) {
      toast.error("Failed to create volume", {
        description: e instanceof Error ? e.message : undefined,
      });
    } finally {
      setBusy(false);
    }
  };

  const remove = async (n: string) => {
    try {
      await docker.deleteVolume(n);
      toast.success("Volume deleted");
    } catch (e) {
      const msg = e instanceof Error ? e.message : undefined;
      // The list can be stale: a container may have started using the volume
      // since it loaded. Say so plainly instead of echoing the daemon error.
      if (msg?.includes("volume is in use")) {
        toast.error("Volume is in use", {
          description:
            "A container still uses it. Stop and remove that container, then try again.",
        });
      } else {
        toast.error("Failed to delete volume", { description: msg });
      }
    }
    await load();
  };

  const appNames = useMemo(
    () => new Map(apps.map((a) => [a.id, a.name])),
    [apps],
  );
  const all = items ?? [];
  const anonCount = all.filter((v) => v.anonymous).length;
  const visible = all.filter((v) => showAnon || !v.anonymous);
  // Backend order already is app-owned, other named, anonymous; group it so
  // Winterflow's volumes don't drown in whatever else runs on the host.
  const groups = (
    [
      ["Winterflow apps", visible.filter((v) => v.app_id)],
      ["Other volumes", visible.filter((v) => !v.app_id && !v.anonymous)],
      ["Anonymous", visible.filter((v) => !v.app_id && v.anonymous)],
    ] as const
  ).filter(([, list]) => list.length > 0);
  const orphanCount = visible.filter((v) => v.orphan).length;
  const trimmed = name.trim();
  const nameError = trimmed ? volumeNameError(trimmed) : "";
  const cols = isAdmin ? 4 : 3;

  const rows = (list: Volume[]) =>
    list.map((v) => {
      const appName = v.app_id ? appNames.get(v.app_id) : undefined;
      return (
        <TableRow key={v.name}>
          {/* w-full + max-w-0 lets the name take the slack and truncate. */}
          <TableCell className="w-full max-w-0">
            <div className="truncate font-mono" title={v.name}>
              {shortVolumeName(v)}
            </div>
            {/* Phones drop the App and Status columns; show them inline. */}
            <div className="mt-1 flex items-center gap-2 text-xs sm:hidden">
              <VolumeStatus volume={v} />
              {(v.app_id || v.project) && (
                <>
                  <span className="text-muted-foreground" aria-hidden>
                    ·
                  </span>
                  <span className="min-w-0 truncate">
                    <VolumeOwner volume={v} appName={appName} />
                  </span>
                </>
              )}
            </div>
          </TableCell>
          <TableCell className="hidden pr-6 sm:table-cell">
            <div className="max-w-48 truncate lg:max-w-64">
              <VolumeOwner volume={v} appName={appName} />
            </div>
          </TableCell>
          <TableCell className="hidden sm:table-cell">
            <VolumeStatus volume={v} />
          </TableCell>
          {isAdmin && (
            <TableCell className="py-0.5 text-right">
              {/* Docker refuses to remove a mounted volume; don't offer it. */}
              {v.in_use ? null : (
                <Button
                  size="icon"
                  variant="ghost"
                  className="size-8 text-muted-foreground hover:text-destructive"
                  aria-label={`Delete volume ${v.name}`}
                  title="Delete volume"
                  onClick={() => setToDelete(v)}
                >
                  <Trash2 className="size-4" />
                </Button>
              )}
            </TableCell>
          )}
        </TableRow>
      );
    });

  const groupRow = (label: string) => (
    <TableRow className="hover:bg-transparent">
      <TableCell
        colSpan={cols}
        className="bg-muted/40 py-1.5 text-xs font-medium text-muted-foreground"
      >
        {label}
      </TableCell>
    </TableRow>
  );

  return (
    <Card>
      <CardHeader className="flex-row flex-wrap items-start justify-between gap-x-4 gap-y-3 space-y-0">
        <div className="min-w-0 space-y-1.5">
          <CardTitle>Volumes</CardTitle>
          <CardDescription>
            Where containers keep data that outlives a redeploy.
          </CardDescription>
        </div>
        <div className="flex items-center gap-2">
          <Button
            size="icon"
            variant="ghost"
            className="size-8 text-muted-foreground"
            aria-label="Refresh volumes"
            title="Refresh volumes"
            onClick={() => void load()}
            disabled={loading}
          >
            <RefreshCw className={cn("size-4", loading && "animate-spin")} />
          </Button>
          {isAdmin && (
            <Dialog open={open} onOpenChange={changeOpen}>
              <DialogTrigger asChild>
                <Button size="sm">
                  <Plus className="size-4" /> Create volume
                </Button>
              </DialogTrigger>
              <DialogContent>
                <form
                  className="grid gap-4"
                  onSubmit={(e) => {
                    e.preventDefault();
                    if (trimmed && !nameError && !busy) void add();
                  }}
                >
                  <DialogHeader>
                    <DialogTitle>Create volume</DialogTitle>
                    <DialogDescription>
                      An empty local volume. To mount it in an app, declare it
                      in the compose file with{" "}
                      <code className="whitespace-nowrap">external: true</code>.
                    </DialogDescription>
                  </DialogHeader>
                  <div className="grid gap-1.5">
                    <Label htmlFor="vol-name">Name</Label>
                    <Input
                      id="vol-name"
                      placeholder="my-volume"
                      autoComplete="off"
                      spellCheck={false}
                      className={cn(
                        "font-mono",
                        nameError &&
                          "border-destructive focus-visible:ring-destructive",
                      )}
                      aria-invalid={nameError !== ""}
                      aria-describedby="vol-name-hint"
                      value={name}
                      onChange={(e) => setName(e.target.value)}
                    />
                    <p
                      id="vol-name-hint"
                      className={cn(
                        "text-xs",
                        nameError ? "text-destructive" : "text-muted-foreground",
                      )}
                    >
                      {nameError ||
                        "Letters, digits, dots, dashes and underscores."}
                    </p>
                  </div>
                  <DialogFooter>
                    <Button
                      type="button"
                      variant="outline"
                      onClick={() => changeOpen(false)}
                    >
                      Cancel
                    </Button>
                    <Button type="submit" disabled={busy || !trimmed || !!nameError}>
                      {busy ? "Creating…" : "Create"}
                    </Button>
                  </DialogFooter>
                </form>
              </DialogContent>
            </Dialog>
          )}
        </div>
      </CardHeader>
      <CardContent>
        {items === null ? (
          <div className="flex h-24 items-center justify-center">
            {loading ? (
              <Spinner />
            ) : (
              <p className="text-sm text-muted-foreground">
                Couldn't load volumes.
              </p>
            )}
          </div>
        ) : (
          <>
            <div className="mb-3 flex flex-wrap items-center justify-between gap-x-4 gap-y-2 text-sm">
              <p className="text-muted-foreground">
                {visible.length === 1 ? "1 volume" : `${visible.length} volumes`}
                {orphanCount > 0 && (
                  <span className="text-amber-700 dark:text-amber-400">
                    {" · "}
                    {orphanCount} orphaned
                  </span>
                )}
              </p>
              {anonCount > 0 && (
                <div className="flex items-center gap-2">
                  <Switch
                    id="vol-show-anon"
                    checked={showAnon}
                    onCheckedChange={setShowAnon}
                  />
                  <Label
                    htmlFor="vol-show-anon"
                    className="font-normal text-muted-foreground"
                  >
                    Show {anonCount} anonymous
                  </Label>
                </div>
              )}
            </div>
            {orphanCount > 0 && (
              <p className="mb-3 text-xs text-muted-foreground">
                Orphaned volumes carry a Winterflow app label, but that app isn't on
                this server — it was deleted, or the volume belongs to another
                Winterflow install on the same Docker host. Check before deleting.
              </p>
            )}
            {visible.length === 0 ? (
              <p className="rounded-lg border border-dashed py-10 text-center text-sm text-muted-foreground">
                {all.length > 0
                  ? "Only anonymous volumes here. Turn on “Show anonymous” to list them."
                  : "No volumes yet. Apps create theirs when they first deploy."}
              </p>
            ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Name</TableHead>
                    <TableHead className="hidden pr-6 sm:table-cell">App</TableHead>
                    <TableHead className="hidden sm:table-cell">
                      Status
                    </TableHead>
                    {isAdmin && (
                      <TableHead className="w-12">
                        <span className="sr-only">Actions</span>
                      </TableHead>
                    )}
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {groups.map(([label, list]) => (
                    <Fragment key={label}>
                      {groups.length > 1 && groupRow(label)}
                      {rows(list)}
                    </Fragment>
                  ))}
                </TableBody>
              </Table>
            )}
          </>
        )}
      </CardContent>
      <AlertDialog
        open={toDelete !== null}
        onOpenChange={(o) => !o && setToDelete(null)}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete this volume?</AlertDialogTitle>
            <AlertDialogDescription>
              <code className="break-all font-mono text-foreground">
                {toDelete?.name}
              </code>{" "}
              and all the data in it will be erased permanently. This cannot be
              undone.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              className={buttonVariants({ variant: "destructive" })}
              onClick={() => {
                if (toDelete) void remove(toDelete.name);
              }}
            >
              Delete volume
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </Card>
  );
}
