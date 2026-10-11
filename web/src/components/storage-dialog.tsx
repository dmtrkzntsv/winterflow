import { useEffect, useRef, useState } from "react";
import { CircleCheck, CircleX } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import type { Docker } from "@/context/use-docker";
import {
  draftError,
  draftFrom,
  STORAGE_TYPES,
  storageVar,
  type Storage,
  type StorageDraft,
  type StorageSaved,
  type StorageType,
} from "@/lib/storages";

type Probe = { state: "idle" | "testing" } | { state: "ok" } | { state: "failed"; error: string };

// StorageDialog adds a storage, or (edit set) changes one: the name and type
// are its identity, so an edit shows them instead of offering them. The
// parent remounts it (key) per opening. Save tests again on the agent and
// stores nothing when that fails. The typed secret never outlives the
// dialog: it is wiped on every close.
export function StorageDialog({
  docker,
  open,
  edit,
  onOpenChange,
  onSaved,
}: {
  docker: Docker;
  open: boolean;
  edit: Storage | null;
  onOpenChange: (open: boolean) => void;
  onSaved: (saved: StorageSaved) => void;
}) {
  const [draft, setDraft] = useState<StorageDraft>(() => draftFrom(edit));
  const [probe, setProbe] = useState<Probe>({ state: "idle" });
  const [busy, setBusy] = useState(false);

  // Answers can arrive after the dialog moved on. A save may run for
  // minutes: when it answers after this dialog closed (or unmounted, the
  // parent remounting a new one), it still reports the save but must not
  // close whatever dialog is open now. A test answer counts only while no
  // newer test or edit superseded it.
  const mounted = useRef(true);
  const session = useRef(0); // bumped on every close
  const probeSeq = useRef(0); // bumped on every test start, edit and close
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);

  const editing = edit !== null;
  const error = draftError(draft, editing);
  const users = edit?.apps?.length ?? 0;

  // Any change makes the last test result stale.
  const set = (patch: Partial<StorageDraft>) => {
    probeSeq.current++;
    setDraft((d) => ({ ...d, ...patch }));
    setProbe({ state: "idle" });
  };

  const changeOpen = (next: boolean) => {
    if (!next) {
      session.current++;
      probeSeq.current++;
      setDraft((d) => ({ ...d, secret: "" }));
      setProbe({ state: "idle" });
    }
    onOpenChange(next);
  };

  const test = async () => {
    const seq = ++probeSeq.current;
    const current = () => mounted.current && seq === probeSeq.current;
    setProbe({ state: "testing" });
    try {
      const res = await docker.testStorage(draft);
      if (current()) setProbe(res.ok ? { state: "ok" } : { state: "failed", error: res.error || "The test failed." });
    } catch (e) {
      if (current()) setProbe({ state: "failed", error: e instanceof Error ? e.message : "The test failed." });
    }
  };

  const save = async () => {
    const opened = session.current;
    setBusy(true);
    try {
      const saved = await docker.saveStorage(draft);
      if (mounted.current && opened === session.current) changeOpen(false);
      onSaved(saved);
    } catch (e) {
      toast.error(editing ? "Couldn't save storage" : "Couldn't add storage", {
        description: e instanceof Error ? e.message : undefined,
      });
    } finally {
      setBusy(false);
    }
  };

  const field = (
    id: string,
    label: string,
    value: string,
    onChange: (v: string) => void,
    props: { placeholder?: string; hint?: string; type?: string } = {},
  ) => (
    <div className="grid gap-1.5">
      <Label htmlFor={id}>{label}</Label>
      <Input
        id={id}
        type={props.type ?? "text"}
        autoComplete="off"
        spellCheck={false}
        placeholder={props.placeholder}
        aria-describedby={props.hint ? `${id}-hint` : undefined}
        value={value}
        onChange={(e) => onChange(e.target.value)}
      />
      {props.hint ? (
        <p id={`${id}-hint`} className="text-xs text-muted-foreground">
          {props.hint}
        </p>
      ) : null}
    </div>
  );

  const name = draft.name.trim();

  return (
    <Dialog open={open} onOpenChange={changeOpen}>
      <DialogContent className="max-h-[calc(100dvh-2rem)] overflow-y-auto sm:max-w-xl">
        <form
          className="grid gap-4"
          onSubmit={(e) => {
            e.preventDefault();
            if (!error && !busy) void save();
          }}
        >
          <DialogHeader>
            <DialogTitle>{editing ? "Edit storage" : "Add storage"}</DialogTitle>
            <DialogDescription>
              {editing ? (
                <>
                  {STORAGE_TYPES[draft.type]}{" "}
                  <code className="font-mono text-foreground">{edit.name}</code>.
                </>
              ) : (
                "A folder or bucket backups and apps on this server can write to."
              )}
            </DialogDescription>
          </DialogHeader>

          {editing ? null : (
            <>
              <Tabs value={draft.type} onValueChange={(v) => set({ type: v as StorageType })}>
                <TabsList>
                  <TabsTrigger value="local">{STORAGE_TYPES.local}</TabsTrigger>
                  <TabsTrigger value="s3">{STORAGE_TYPES.s3}</TabsTrigger>
                </TabsList>
              </Tabs>
              {field("st-name", "Name", draft.name, (v) => set({ name: v.toLowerCase() }), {
                placeholder: "backup-disk",
                hint: name
                  ? `Compose files use it as \${${storageVar(name, "…")}}.`
                  : "Lowercase letters, digits and hyphens.",
              })}
            </>
          )}

          {draft.type === "local" ? (
            field("st-path", "Folder", draft.path, (v) => set({ path: v }), {
              placeholder: "/mnt/backup",
              hint: "An existing folder, usually under /mnt or /media.",
            })
          ) : (
            <>
              {field("st-endpoint", "Endpoint", draft.endpoint, (v) => set({ endpoint: v }), {
                placeholder: "https://s3.example.com",
              })}
              <div className="grid gap-4 sm:grid-cols-2">
                {field("st-region", "Region", draft.region, (v) => set({ region: v }), { placeholder: "auto" })}
                {field("st-bucket", "Bucket", draft.bucket, (v) => set({ bucket: v }))}
              </div>
              {field("st-prefix", "Prefix", draft.prefix, (v) => set({ prefix: v }), {
                placeholder: "winterflow/",
                hint: "Optional: everything is written under this folder of the bucket.",
              })}
              {field("st-key", "Access key", draft.accessKeyId, (v) => set({ accessKeyId: v }))}
              {field("st-secret", "Secret key", draft.secret, (v) => set({ secret: v }), {
                type: "password",
                hint: editing
                  ? "Leave empty to keep."
                  : "Encrypted in your browser; only this server can read it.",
              })}
              <label className="flex items-center gap-2 text-sm">
                <Checkbox
                  checked={draft.pathStyle}
                  onCheckedChange={(v) => set({ pathStyle: v === true })}
                  aria-label="Path-style addressing"
                />
                Path-style addressing (MinIO and some providers)
              </label>
            </>
          )}

          {editing && users > 0 ? (
            <p className="rounded-md bg-amber-500/10 px-3 py-2 text-sm text-amber-800 dark:text-amber-300">
              Up to {users === 1 ? "1 running app" : `${users} running apps`} will be redeployed.
            </p>
          ) : null}

          <div className="flex min-h-9 flex-wrap items-center gap-3" aria-live="polite">
            <Button
              type="button"
              variant="outline"
              disabled={error !== "" || probe.state === "testing"}
              onClick={() => void test()}
            >
              {probe.state === "testing" ? "Testing…" : "Test connection"}
            </Button>
            {probe.state === "ok" ? (
              <span className="flex items-center gap-1.5 text-sm text-emerald-700 dark:text-emerald-400">
                <CircleCheck className="size-4" /> Connection works
              </span>
            ) : probe.state === "failed" ? (
              <span className="flex items-start gap-1.5 text-sm text-destructive [overflow-wrap:anywhere]">
                <CircleX className="mt-0.5 size-4 shrink-0" /> {probe.error}
              </span>
            ) : null}
          </div>
          {error && (name || draft.path || draft.endpoint) ? (
            <p className="text-xs text-destructive">{error}</p>
          ) : null}

          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => changeOpen(false)}>
              Cancel
            </Button>
            <Button type="submit" disabled={error !== "" || busy}>
              {busy ? "Saving…" : "Save"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
