import { Fragment, useCallback, useEffect, useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { Plus, RefreshCw, Trash2 } from "lucide-react";
import { toast } from "sonner";

import { StatusDot } from "@/components/app-status-badge";
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
import { type Docker, type Volume } from "@/context/use-docker";
import { useApps } from "@/context/use-apps";
import { cn } from "@/lib/utils";
import {
  groupVolumes,
  shortVolumeName,
  volumeNameError,
} from "@/lib/volumes";
import { useServerPath } from "@/context/use-server-path";

// Same dot + label language as AppStatusIndicator: the normal state stays
// quiet, only Orphaned (data nobody owns any more) draws the eye.
function VolumeStatus({ volume }: { volume: Volume }) {
  const [label, dot, text] = volume.in_use
    ? ["In use", "bg-emerald-500", ""]
    : volume.orphan
      ? ["Orphaned", "bg-amber-400", "text-amber-700 dark:text-amber-400"]
      : ["Unused", "bg-muted-foreground/40", "text-muted-foreground"];
  return (
    <StatusDot
      label={label}
      dotClassName={dot}
      className={cn("whitespace-nowrap", text)}
    />
  );
}

function VolumeOwner({ volume, appName }: { volume: Volume; appName?: string }) {
  const toServer = useServerPath();
  if (volume.app_id && appName) {
    return (
      <Link
        to={toServer(`/app/${volume.app_id}`)}
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

// CreateVolumeDialog owns its open/name/busy state; closing it (any path)
// resets the name.
function CreateVolumeDialog({
  docker,
  onCreated,
}: {
  docker: Docker;
  onCreated: () => Promise<void>;
}) {
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [name, setName] = useState("");
  const trimmed = name.trim();
  const nameError = trimmed ? volumeNameError(trimmed) : "";
  const canCreate = trimmed !== "" && !nameError && !busy;

  // Single close path: Radix fires onOpenChange only for its own dismissals,
  // so the Cancel button must go through here to reset the name too.
  const changeOpen = (o: boolean) => {
    setOpen(o);
    if (!o) setName("");
  };

  const add = async () => {
    setBusy(true);
    try {
      await docker.createVolume(trimmed);
      toast.success("Volume created");
      changeOpen(false);
      await onCreated();
    } catch (e) {
      toast.error("Failed to create volume", {
        description: e instanceof Error ? e.message : undefined,
      });
    } finally {
      setBusy(false);
    }
  };

  return (
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
            if (canCreate) void add();
          }}
        >
          <DialogHeader>
            <DialogTitle>Create volume</DialogTitle>
            <DialogDescription>
              An empty local volume. To mount it in an app, declare it in the
              compose file with{" "}
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
                nameError && "border-destructive focus-visible:ring-destructive",
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
              {nameError || "Letters, digits, dots, dashes and underscores."}
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
            <Button type="submit" disabled={!canCreate}>
              {busy ? "Creating…" : "Create"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

// DeleteVolumeDialog confirms erasing one volume; open while volume is set.
function DeleteVolumeDialog({
  volume,
  onClose,
  onConfirm,
}: {
  volume: Volume | null;
  onClose: () => void;
  onConfirm: (name: string) => void;
}) {
  return (
    <AlertDialog open={volume !== null} onOpenChange={(o) => !o && onClose()}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Delete this volume?</AlertDialogTitle>
          <AlertDialogDescription>
            <code className="break-all font-mono text-foreground">
              {volume?.name}
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
              if (volume) onConfirm(volume.name);
            }}
          >
            Delete volume
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}

export function VolumesCard({ docker }: { docker: Docker }) {
  const { apps } = useApps();
  const [items, setItems] = useState<Volume[] | null>(null);
  const [loading, setLoading] = useState(true);
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

  const remove = async (n: string) => {
    try {
      await docker.deleteVolume(n);
      toast.success("Volume deleted");
    } catch (e) {
      // The list may be stale (a container started using the volume since
      // it loaded); Docker's reason says so, and the reload below shows it.
      toast.error("Couldn't delete volume", {
        description: e instanceof Error ? e.message : undefined,
      });
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
  const groups = groupVolumes(visible);
  const orphanCount = visible.filter((v) => v.orphan).length;

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
        </TableRow>
      );
    });

  const groupRow = (label: string) => (
    <TableRow className="hover:bg-transparent">
      <TableCell
        colSpan={4}
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
          <CreateVolumeDialog docker={docker} onCreated={load} />
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
                    <TableHead className="w-12">
                      <span className="sr-only">Actions</span>
                    </TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {groups.map(({ label, volumes }) => (
                    <Fragment key={label}>
                      {groups.length > 1 && groupRow(label)}
                      {rows(volumes)}
                    </Fragment>
                  ))}
                </TableBody>
              </Table>
            )}
          </>
        )}
      </CardContent>
      <DeleteVolumeDialog
        volume={toDelete}
        onClose={() => setToDelete(null)}
        onConfirm={(n) => void remove(n)}
      />
    </Card>
  );
}
