import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { ChevronDown, ChevronRight, Cloud, Folder, HardDrive, Plus } from "lucide-react";
import { toast } from "sonner";

import { AppLinks } from "@/components/app-links";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { EmptyState } from "@/components/empty-state";
import { ListPlaceholder } from "@/components/list-placeholder";
import { OrphansDialog } from "@/components/orphans-dialog";
import { PageHeader } from "@/components/page-header";
import { RefreshButton } from "@/components/refresh-button";
import { RowMenu } from "@/components/row-menu";
import { Row, RowList } from "@/components/section";
import { StorageDialog } from "@/components/storage-dialog";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { useApps } from "@/context/use-apps";
import { useBackups } from "@/context/use-backups";
import type { Docker } from "@/context/use-docker";
import { useConfirmTarget } from "@/hooks/use-confirm-target";
import { returnFocusTo } from "@/lib/focus";
import { STORAGE_TYPES, type Storage, type StorageSaved } from "@/lib/storages";

const byName = (a: Storage, b: Storage) => a.name.localeCompare(b.name);

// listed joins names for a sentence: "A", "A and B", "A, B and C".
const listed = (names: string[]) => new Intl.ListFormat("en-GB", { type: "conjunction" }).format(names);

// StoragesSettings is Settings → Server → Storages: the folders and S3
// buckets backups and apps on this server write to, each with the apps
// using it; edit, look at the backups of deleted apps kept in it, or
// remove one nothing uses.
export function StoragesSettings({ docker }: { docker: Docker }) {
  const { apps } = useApps();
  const backups = useBackups();
  const [items, setItems] = useState<Storage[] | null>(null);
  const [loading, setLoading] = useState(true);
  const [dialogOpen, setDialogOpen] = useState(false);
  const [edit, setEdit] = useState<Storage | null>(null);
  const [dialogKey, setDialogKey] = useState(0);
  const removal = useConfirmTarget<Storage>();
  // The storage whose backups of deleted apps are shown.
  const [orphansOf, setOrphansOf] = useState<string | null>(null);
  // The dialogs have no trigger of their own: focus goes back to what
  // opened them, or to Add storage when that left with its row.
  const opener = useRef<HTMLElement | null>(null);
  const addButton = useRef<HTMLButtonElement | null>(null);
  const orphansLink = useRef<HTMLButtonElement | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      setItems(await docker.listStorages());
    } catch (e) {
      toast.error("Couldn't load storages", { description: e instanceof Error ? e.message : undefined });
    } finally {
      setLoading(false);
    }
  }, [docker]);

  useEffect(() => {
    void load();
  }, [load]);

  const names = useMemo(() => new Map(apps.map((a) => [a.id, a.name])), [apps]);

  // A save answers with the stored storage: patch the list in place.
  const saved = ({ storage, redeployed, warnings }: StorageSaved) => {
    const done = edit ? "Storage saved" : "Storage added";
    const description =
      redeployed.length > 0 ? `Redeployed ${redeployed.map((id) => names.get(id) ?? "an app").join(", ")}.` : undefined;
    if (warnings.length > 0) {
      // It asks for action (each warning names its app): it stays until dismissed.
      toast.warning(`${done}, but not every app was redeployed`, {
        // One line each (toast descriptions keep newlines: index.css).
        description: [description, ...warnings].filter(Boolean).join("\n"),
        duration: Infinity,
        closeButton: true,
      });
    } else {
      toast.success(done, { description });
    }
    setItems((prev) => [...(prev ?? []).filter((s) => s.name !== storage.name), storage].sort(byName));
  };

  const remove = async (name: string) => {
    try {
      await docker.deleteStorage(name);
      toast.success("Storage removed");
      setItems((prev) => prev && prev.filter((s) => s.name !== name));
    } catch (e) {
      toast.error("Couldn't remove storage", { description: e instanceof Error ? e.message : undefined });
      throw e;
    }
  };

  const openDialog = (row: Storage | null, from: HTMLElement | null) => {
    opener.current = from;
    setEdit(row);
    setDialogKey((k) => k + 1);
    setDialogOpen(true);
  };

  return (
    <div className="space-y-6">
      <PageHeader
        title="Storages"
        subtitle="Folders and S3 buckets that backups and apps on this server can write to."
        actions={
          <>
            <RefreshButton label="Refresh storages" loading={loading} onClick={() => void load()} />
            <Button ref={addButton} onClick={(e) => openDialog(null, e.currentTarget)}>
              <Plus className="size-4" /> Add storage
            </Button>
          </>
        }
      />
      {items === null ? (
        <ListPlaceholder loading={loading} failed="Couldn't load storages." onRetry={() => void load()} />
      ) : items.length === 0 ? (
        <EmptyState
          icon={HardDrive}
          title="No storages yet. Add a folder, such as one on a USB disk under /mnt, or an S3 bucket."
        />
      ) : (
        <RowList label="Storages">
          {items.map((s) => {
            const used = s.apps ?? [];
            // Why the storage can't be removed (null: it can).
            const blocked =
              used.length > 0
                ? `Used by ${listed(used.map((id) => names.get(id) ?? "an app"))} — remove the references first`
                : null;
            const Icon = s.type === "s3" ? Cloud : Folder;
            return (
              <Row
                key={s.name}
                leading={
                  <span className="flex size-9 items-center justify-center rounded-lg bg-muted">
                    <Icon className="size-4 text-muted-foreground" aria-hidden />
                  </span>
                }
                title={<span className="font-mono">{s.name}</span>}
                description={`${STORAGE_TYPES[s.type]} · ${s.location}`}
                meta={<AppLinks ids={used} names={names} />}
                actions={
                  <RowMenu
                    label={`Actions for ${s.name}`}
                    items={[
                      { label: "Edit", onSelect: (trigger) => openDialog(s, trigger) },
                      {
                        label: "Remove",
                        destructive: true,
                        title: blocked ?? undefined,
                        onSelect: (trigger) => {
                          // In use: the item says why (title), and choosing it says so on screen.
                          if (blocked) {
                            toast.warning(`Can't remove ${s.name}`, { description: `${blocked}.` });
                            return;
                          }
                          opener.current = trigger;
                          removal.ask(s);
                        },
                      },
                    ]}
                  />
                }
              />
            );
          })}
        </RowList>
      )}
      {items !== null && items.length > 0 ? (
        // Copies of deleted apps stay in their storage: this link below the
        // list is the one way in. With several storages it asks which one.
        items.length === 1 ? (
          <Button
            ref={orphansLink}
            variant="link"
            className="h-auto gap-1 p-0 max-sm:h-10"
            onClick={(e) => {
              opener.current = e.currentTarget;
              setOrphansOf(items[0].name);
            }}
          >
            Backups of deleted apps
            <ChevronRight className="size-4" aria-hidden />
          </Button>
        ) : (
          <DropdownMenu modal={false}>
            <DropdownMenuTrigger asChild>
              <Button ref={orphansLink} variant="link" className="h-auto gap-1 p-0 max-sm:h-10">
                Backups of deleted apps
                <ChevronDown className="size-4" aria-hidden />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="start">
              {items.map((s) => (
                <DropdownMenuItem
                  key={s.name}
                  className="font-mono"
                  onSelect={() => {
                    opener.current = orphansLink.current;
                    setOrphansOf(s.name);
                  }}
                >
                  {s.name}
                </DropdownMenuItem>
              ))}
            </DropdownMenuContent>
          </DropdownMenu>
        )
      ) : null}
      <StorageDialog
        key={dialogKey}
        docker={docker}
        open={dialogOpen}
        edit={edit}
        existing={(items ?? []).map((s) => s.name)}
        usedBy={(edit?.apps ?? []).map((id) => names.get(id) ?? "an app")}
        returnFocus={opener}
        onOpenChange={setDialogOpen}
        onSaved={saved}
      />
      {orphansOf ? (
        <OrphansDialog
          key={orphansOf}
          backups={backups}
          storage={orphansOf}
          open
          returnFocus={opener}
          onOpenChange={(o) => !o && setOrphansOf(null)}
        />
      ) : null}
      <ConfirmDialog
        open={removal.open}
        onOpenChange={removal.onOpenChange}
        title="Remove this storage?"
        description={
          <>
            <code className="font-mono text-foreground [overflow-wrap:anywhere]">{removal.target?.name}</code> is removed from
            this server. Nothing stored in it is deleted.
          </>
        }
        confirmLabel="Remove storage"
        pendingLabel="Removing…"
        onCloseAutoFocus={returnFocusTo(opener, addButton)}
        onConfirm={() => (removal.target ? remove(removal.target.name) : undefined)}
      />
    </div>
  );
}
