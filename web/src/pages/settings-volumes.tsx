import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { Link } from "react-router-dom";
import { Plus } from "lucide-react";
import { toast } from "sonner";

import { ConfirmDialog } from "@/components/confirm-dialog";
import { EmptyState } from "@/components/empty-state";
import { ListPlaceholder } from "@/components/list-placeholder";
import { RefreshButton } from "@/components/refresh-button";
import { RemoveButton } from "@/components/remove-button";
import { ResponsiveDialog } from "@/components/responsive-dialog";
import { Row, RowList, Section } from "@/components/section";
import { StatusDot } from "@/components/status";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { useApps } from "@/context/use-apps";
import type { Docker, Volume } from "@/context/use-docker";
import { useServerPath } from "@/context/use-server-path";
import { useConfirmTarget } from "@/hooks/use-confirm-target";
import { returnFocusTo } from "@/lib/focus";
import { appSub } from "@/lib/routes";
import { cn } from "@/lib/utils";
import { groupVolumes, shortVolumeName, volumeNameError, volumeState } from "@/lib/volumes";

const errorText = (e: unknown) => (e instanceof Error ? e.message : undefined);

// ownerOf names who a volume belongs to: its app (a link), a deleted app,
// or the compose project; nothing for a plain volume.
function ownerOf(volume: Volume, appName: string | undefined, appHref: (id: string) => string): ReactNode {
  if (volume.app_id && appName) {
    return (
      <Link to={appHref(volume.app_id)} className="font-medium underline-offset-4 hover:underline">
        {appName}
      </Link>
    );
  }
  if (volume.orphan) return <span title={`No app ${volume.app_id} on this server`}>App not on this server</span>;
  if (volume.project) return <span title="Docker Compose project">{volume.project}</span>;
  return null;
}

// CreateVolumeDialog asks for a volume's name; closing it (any way) clears it.
function CreateVolumeDialog({
  docker,
  open,
  onOpenChange,
  onCreated,
}: {
  docker: Docker;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onCreated: () => Promise<void>;
}) {
  const [busy, setBusy] = useState(false);
  const [name, setName] = useState("");
  const trimmed = name.trim();
  const nameError = trimmed ? volumeNameError(trimmed) : "";
  const canCreate = trimmed !== "" && !nameError && !busy;

  const changeOpen = (next: boolean) => {
    if (!next) setName("");
    onOpenChange(next);
  };

  const create = async () => {
    setBusy(true);
    try {
      await docker.createVolume(trimmed);
      toast.success("Volume created");
      changeOpen(false);
      await onCreated();
    } catch (e) {
      toast.error("Couldn't create the volume", { description: errorText(e) });
    } finally {
      setBusy(false);
    }
  };

  return (
    <ResponsiveDialog
      open={open}
      onOpenChange={changeOpen}
      title="Create volume"
      description={
        <>
          An empty local volume. To mount it in an app, declare it in the compose file with{" "}
          <code className="whitespace-nowrap">external: true</code>.
        </>
      }
      footer={
        <>
          <Button type="button" variant="outline" onClick={() => changeOpen(false)}>
            Cancel
          </Button>
          <Button type="submit" form="volume-form" disabled={!canCreate}>
            {busy ? "Creating…" : "Create"}
          </Button>
        </>
      }
    >
      <form
        id="volume-form"
        className="grid gap-1.5"
        onSubmit={(e) => {
          e.preventDefault();
          if (canCreate) void create();
        }}
      >
        <Label htmlFor="vol-name">Name</Label>
        <Input
          id="vol-name"
          placeholder="my-volume"
          autoComplete="off"
          spellCheck={false}
          className={cn("font-mono", nameError && "border-destructive focus-visible:ring-destructive")}
          aria-invalid={nameError !== ""}
          aria-describedby="vol-name-hint"
          value={name}
          onChange={(e) => setName(e.target.value)}
        />
        <p id="vol-name-hint" className={cn("text-xs", nameError ? "text-destructive" : "text-muted-foreground")}>
          {nameError || "Letters, digits, dots, dashes and underscores."}
        </p>
      </form>
    </ResponsiveDialog>
  );
}

// VolumesSection is Settings → Docker → Volumes: Winterflow apps' volumes
// first, then the rest of the host's, anonymous ones behind a switch. Any
// volume no container mounts can be deleted, after a confirmation.
export function VolumesSection({ docker }: { docker: Docker }) {
  const { apps } = useApps();
  const toServer = useServerPath();
  const [items, setItems] = useState<Volume[] | null>(null);
  const [loading, setLoading] = useState(true);
  const [showAnon, setShowAnon] = useState(false);
  const [creating, setCreating] = useState(false);
  const removal = useConfirmTarget<Volume>();
  // A deleted row takes its button along: focus then goes to Create volume.
  const opener = useRef<HTMLElement | null>(null);
  const createButton = useRef<HTMLButtonElement | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      setItems(await docker.listVolumes());
    } catch (e) {
      toast.error("Couldn't load volumes", { description: errorText(e) });
    } finally {
      setLoading(false);
    }
  }, [docker]);

  useEffect(() => {
    void load();
  }, [load]);

  // A failed delete lists again (the list may be stale: a container started
  // using the volume since it loaded) and keeps the confirmation open.
  const remove = async (name: string) => {
    try {
      await docker.deleteVolume(name);
      toast.success("Volume deleted");
      await load();
    } catch (e) {
      toast.error("Couldn't delete the volume", { description: errorText(e) });
      await load();
      throw e;
    }
  };

  const appNames = useMemo(() => new Map(apps.map((a) => [a.id, a.name])), [apps]);
  const all = items ?? [];
  const anonCount = all.filter((v) => v.anonymous).length;
  const visible = all.filter((v) => showAnon || !v.anonymous);
  const groups = groupVolumes(visible);
  const unusedByApps = visible.filter((v) => v.orphan).length;

  return (
    <Section
      title="Volumes"
      description="Where containers keep data that outlives a redeploy."
      actions={
        <>
          <RefreshButton label="Refresh volumes" loading={loading} onClick={() => void load()} />
          <Button ref={createButton} size="sm" variant="outline" onClick={() => setCreating(true)}>
            <Plus className="size-4" /> Create volume
          </Button>
        </>
      }
    >
      {items === null ? (
        <ListPlaceholder loading={loading} failed="Couldn't load volumes." onRetry={() => void load()} />
      ) : (
        <div className="space-y-3">
          <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2 text-sm">
            <p className="text-muted-foreground">
              {visible.length === 1 ? "1 volume" : `${visible.length} volumes`}
              {unusedByApps > 0 ? <span className="text-foreground">{` · ${unusedByApps} not used by any app`}</span> : null}
            </p>
            {anonCount > 0 ? (
              <div className="flex items-center gap-2">
                <Switch id="vol-show-anon" checked={showAnon} onCheckedChange={setShowAnon} />
                <Label htmlFor="vol-show-anon" className="font-normal text-muted-foreground">
                  Show {anonCount} anonymous
                </Label>
              </div>
            ) : null}
          </div>
          {unusedByApps > 0 ? (
            <p className="text-xs text-muted-foreground">
              A volume not used by any app carries a Winterflow app label, but that app isn't on this server: it was
              deleted, or the volume belongs to another Winterflow install on the same Docker host. Check before deleting.
            </p>
          ) : null}
          {visible.length === 0 ? (
            <EmptyState
              title={
                all.length > 0
                  ? "Only anonymous volumes here. Turn on “Show anonymous” to list them."
                  : "No volumes yet. Apps create theirs when they first deploy."
              }
            />
          ) : (
            groups.map(({ label, volumes }) => (
              <div key={label} className="space-y-1.5">
                {groups.length > 1 ? <h3 className="px-1 text-xs font-medium text-muted-foreground">{label}</h3> : null}
                <RowList label={label}>
                  {volumes.map((v) => {
                    const state = volumeState(v);
                    return (
                      <Row
                        key={v.name}
                        title={
                          <span className="font-mono" title={v.name}>
                            {shortVolumeName(v)}
                          </span>
                        }
                        description={ownerOf(v, v.app_id ? appNames.get(v.app_id) : undefined, (id) => toServer(appSub(id)))}
                        meta={
                          <span className="inline-flex items-center gap-1.5 whitespace-nowrap">
                            <StatusDot tone={state.tone} />
                            {state.label}
                          </span>
                        }
                        actions={
                          // Docker refuses to remove a mounted volume; don't offer it.
                          v.in_use ? null : (
                            <RemoveButton
                              label={`Delete volume ${v.name}`}
                              onClick={(trigger) => {
                                opener.current = trigger;
                                removal.ask(v);
                              }}
                            />
                          )
                        }
                      />
                    );
                  })}
                </RowList>
              </div>
            ))
          )}
        </div>
      )}
      <CreateVolumeDialog docker={docker} open={creating} onOpenChange={setCreating} onCreated={load} />
      <ConfirmDialog
        open={removal.open}
        onOpenChange={removal.onOpenChange}
        title="Delete this volume?"
        description={
          <>
            <code className="break-all font-mono text-foreground">{removal.target?.name}</code> and all the data in it are
            erased for good. This can't be undone.
          </>
        }
        confirmLabel="Delete volume"
        pendingLabel="Deleting…"
        onCloseAutoFocus={(e) => returnFocusTo(opener, createButton)(e)}
        onConfirm={() => (removal.target ? remove(removal.target.name) : undefined)}
      />
    </Section>
  );
}
