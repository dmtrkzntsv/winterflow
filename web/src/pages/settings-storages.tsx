import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Archive, Pencil, Plus, RefreshCw, Trash2 } from "lucide-react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { buttonVariants } from "@/components/ui/button-variants";
import { Spinner } from "@/components/ui/spinner";
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
import { AppLinks } from "@/components/app-links";
import { OrphansDialog } from "@/components/orphans-dialog";
import { StorageDialog } from "@/components/storage-dialog";
import { useBackups } from "@/context/use-backups";
import { type Docker } from "@/context/use-docker";
import { useApps } from "@/context/use-apps";
import { returnFocusTo } from "@/lib/focus";
import { STORAGE_TYPES, type Storage, type StorageSaved } from "@/lib/storages";
import { cn } from "@/lib/utils";

const byName = (a: Storage, b: Storage) => a.name.localeCompare(b.name);

// listed joins names for a sentence: "A", "A and B", "A, B and C".
const listed = (names: string[]) => new Intl.ListFormat("en-GB", { type: "conjunction" }).format(names);

export function StoragesCard({ docker }: { docker: Docker }) {
  const { apps } = useApps();
  const [items, setItems] = useState<Storage[] | null>(null);
  const [loading, setLoading] = useState(true);
  const [dialogOpen, setDialogOpen] = useState(false);
  const [edit, setEdit] = useState<Storage | null>(null);
  const [dialogKey, setDialogKey] = useState(0);
  const [toDelete, setToDelete] = useState<Storage | null>(null);
  // The storage whose backups of apps not on this server are shown.
  const [orphansOf, setOrphansOf] = useState<string | null>(null);
  const backups = useBackups();
  // The dialogs have no trigger of their own (the storage dialog remounts
  // per opening): focus goes back to the button that opened them, or to
  // Add storage when that button left with its row.
  const opener = useRef<HTMLElement | null>(null);
  const addButton = useRef<HTMLButtonElement | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      setItems(await docker.listStorages());
    } catch (e) {
      toast.error("Failed to load storages", {
        description: e instanceof Error ? e.message : undefined,
      });
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
      redeployed.length > 0
        ? `Redeployed ${redeployed.map((id) => names.get(id) ?? id).join(", ")}.`
        : undefined;
    if (warnings.length > 0) {
      // It asks for action (each warning names its app): it stays until
      // dismissed.
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
      // Focus on the removed row's button would fall to the page.
      if (opener.current && opener.current === document.activeElement) addButton.current?.focus();
      setItems((prev) => prev && prev.filter((s) => s.name !== name));
    } catch (e) {
      toast.error("Couldn't remove storage", {
        description: e instanceof Error ? e.message : undefined,
      });
    }
  };

  const openDialog = (row: Storage | null, from: HTMLElement) => {
    opener.current = from;
    setEdit(row);
    setDialogKey((k) => k + 1);
    setDialogOpen(true);
  };

  return (
    <Card>
      <CardHeader className="flex-row flex-wrap items-start justify-between gap-x-4 gap-y-3 space-y-0">
        {/* basis-72: the text shrinks beside the actions, and the actions
            wrap under it only on narrow screens. */}
        <div className="min-w-0 flex-1 basis-72 space-y-1.5">
          <CardTitle>Storages</CardTitle>
          <CardDescription>
            Folders and S3-compatible buckets that backups and apps on this server can
            write to. Compose files use them as{" "}
            <code className="font-mono">{"${WF_STORAGE_<NAME>_<FIELD>}"}</code>.
          </CardDescription>
        </div>
        <div className="flex items-center gap-2">
          <Button
            size="icon"
            variant="ghost"
            className="size-8 text-muted-foreground"
            aria-label="Refresh storages"
            title="Refresh storages"
            onClick={() => void load()}
            disabled={loading}
          >
            <RefreshCw className={cn("size-4", loading && "animate-spin")} />
          </Button>
          <Button ref={addButton} size="sm" onClick={(e) => openDialog(null, e.currentTarget)}>
            <Plus className="size-4" /> Add storage
          </Button>
        </div>
      </CardHeader>
      <CardContent>
        {items === null ? (
          <div className="flex h-24 items-center justify-center">
            {loading ? <Spinner /> : <p className="text-sm text-muted-foreground">Couldn't load storages.</p>}
          </div>
        ) : items.length === 0 ? (
          <p className="rounded-lg border border-dashed px-4 py-10 text-center text-sm text-muted-foreground">
            No storages yet. Add a folder, such as one on a USB disk under <code className="font-mono">/mnt</code>, or
            an S3 bucket.
          </p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead className="hidden sm:table-cell">Type</TableHead>
                <TableHead className="hidden md:table-cell">Location</TableHead>
                <TableHead className="hidden sm:table-cell">Used by</TableHead>
                <TableHead className="w-px">
                  <span className="sr-only">Actions</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.map((s) => {
                const used = s.apps ?? [];
                // Why the storage can't be removed (null: it can).
                const blocked =
                  used.length > 0
                    ? `Used by ${listed(used.map((id) => names.get(id) ?? id))} — remove the references first`
                    : null;
                const type = (
                  <Badge variant="outline" className="whitespace-nowrap">
                    {STORAGE_TYPES[s.type]}
                  </Badge>
                );
                return (
                  <TableRow key={s.name}>
                    <TableCell>
                      <div className="font-mono [overflow-wrap:anywhere]">{s.name}</div>
                      {/* Phones drop the Type, Location and Used by
                          columns (the name gets the width); keep them
                          visible inline. */}
                      <div className="mt-1 sm:hidden">{type}</div>
                      <div className="mt-1 font-mono text-xs text-muted-foreground [overflow-wrap:anywhere] md:hidden">
                        {s.location}
                      </div>
                      {used.length > 0 ? (
                        <div className="mt-1 sm:hidden">
                          <AppLinks ids={used} names={names} chips />
                        </div>
                      ) : null}
                      {/* Copies of deleted apps stay in the storage: a labeled
                          entry point, so they can be found. */}
                      <Button
                        variant="link"
                        size="sm"
                        className="mt-1 h-auto p-0 text-xs text-muted-foreground hover:text-foreground"
                        aria-label={`Other apps' backups in ${s.name}`}
                        onClick={(e) => {
                          opener.current = e.currentTarget;
                          setOrphansOf(s.name);
                        }}
                      >
                        <Archive className="size-3.5" /> Other apps' backups
                      </Button>
                    </TableCell>
                    <TableCell className="hidden sm:table-cell">{type}</TableCell>
                    <TableCell className="hidden font-mono text-xs [overflow-wrap:anywhere] md:table-cell">
                      {s.location}
                    </TableCell>
                    <TableCell className="hidden sm:table-cell">
                      <AppLinks ids={used} names={names} chips />
                    </TableCell>
                    <TableCell className="whitespace-nowrap py-0.5 text-right">
                      <Button
                        size="icon"
                        variant="ghost"
                        className="size-8 text-muted-foreground"
                        aria-label={`Edit storage ${s.name}`}
                        title="Edit storage"
                        onClick={(e) => openDialog(s, e.currentTarget)}
                      >
                        <Pencil className="size-4" />
                      </Button>
                      {/* In use: aria-disabled rather than disabled, so the
                          button keeps hover (title) and focus, screen reader
                          users hear why (aria-describedby), and a click, tap
                          or Enter says why on screen. */}
                      <Button
                        size="icon"
                        variant="ghost"
                        className="size-8 text-muted-foreground hover:text-destructive aria-disabled:cursor-not-allowed aria-disabled:opacity-50 aria-disabled:hover:text-muted-foreground"
                        aria-label={`Remove storage ${s.name}`}
                        aria-disabled={blocked ? true : undefined}
                        aria-describedby={blocked ? `storage-in-use-${s.name}` : undefined}
                        title={blocked ?? "Remove storage"}
                        onClick={(e) => {
                          if (blocked) {
                            toast.warning(`Can't remove ${s.name}`, { description: `${blocked}.` });
                            return;
                          }
                          opener.current = e.currentTarget;
                          setToDelete(s);
                        }}
                      >
                        <Trash2 className="size-4" />
                      </Button>
                      {blocked ? (
                        <span id={`storage-in-use-${s.name}`} hidden>
                          {blocked}
                        </span>
                      ) : null}
                    </TableCell>
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        )}
      </CardContent>
      <StorageDialog
        key={dialogKey}
        docker={docker}
        open={dialogOpen}
        edit={edit}
        existing={(items ?? []).map((s) => s.name)}
        usedBy={(edit?.apps ?? []).map((id) => names.get(id) ?? id)}
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
      <AlertDialog open={toDelete !== null} onOpenChange={(o) => !o && setToDelete(null)}>
        <AlertDialogContent onCloseAutoFocus={returnFocusTo(opener, addButton)}>
          <AlertDialogHeader>
            <AlertDialogTitle>Remove this storage?</AlertDialogTitle>
            <AlertDialogDescription>
              <code className="font-mono text-foreground [overflow-wrap:anywhere]">{toDelete?.name}</code> is removed from this
              server. Nothing stored in it is deleted.
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
              Remove storage
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </Card>
  );
}
