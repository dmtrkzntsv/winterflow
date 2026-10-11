import { useEffect, useMemo, useRef, useState, type RefObject } from "react";
import { CircleCheck, CircleX } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import type { Docker } from "@/context/use-docker";
import { returnFocusTo } from "@/lib/focus";
import {
  DRAFT_FIELDS,
  draftFrom,
  draftProblem,
  pathStyleDefault,
  STORAGE_TYPES,
  storageVar,
  type DraftField,
  type Storage,
  type StorageDraft,
  type StorageSaved,
  type StorageType,
} from "@/lib/storages";

type Probe = { state: "idle" | "testing" } | { state: "ok" } | { state: "failed"; error: string };

// fieldId is the id of a field's input.
const fieldId = (field: DraftField) => `st-${field}`;

// COPY is the wording that differs by type.
const COPY: Record<StorageType, { namePlaceholder: string; test: string; testing: string; ok: string }> = {
  local: { namePlaceholder: "e.g. backup-disk", test: "Check folder", testing: "Checking…", ok: "Folder is writable" },
  s3: { namePlaceholder: "e.g. offsite", test: "Test connection", testing: "Testing…", ok: "Connection works" },
};

const errorOf = (e: unknown) => (e instanceof Error ? e.message : "");

// StorageDialog adds a storage, or (edit set) changes one: the name and type
// are its identity, so an edit shows them instead of offering them. The
// parent remounts it (key) per opening. Save tests again on the agent and
// stores nothing when that fails. The typed secret never outlives the
// dialog: it is wiped on every close. existing are the server's storage
// names (an add can't reuse one); usedBy names the apps using the edited
// storage; returnFocus is where focus goes back to on close (the dialog has
// no trigger of its own).
export function StorageDialog({
  docker,
  open,
  edit,
  existing = [],
  usedBy,
  returnFocus,
  onOpenChange,
  onSaved,
}: {
  docker: Docker;
  open: boolean;
  edit: Storage | null;
  existing?: readonly string[];
  usedBy?: readonly string[];
  returnFocus?: RefObject<HTMLElement | null>;
  onOpenChange: (open: boolean) => void;
  onSaved: (saved: StorageSaved) => void;
}) {
  const initial = useMemo(() => draftFrom(edit), [edit]);
  const [draft, setDraft] = useState<StorageDraft>(initial);
  const [probe, setProbe] = useState<Probe>({ state: "idle" });
  const [busy, setBusy] = useState(false);
  const [saveError, setSaveError] = useState("");
  // The furthest field (DRAFT_FIELDS index) the user changed or left: a
  // problem shows once the user reached its field.
  const [reached, setReached] = useState(-1);

  // Answers can arrive after the dialog moved on. A save may run for
  // minutes: when it answers after this dialog closed (or unmounted, the
  // parent remounting a new one), it still reports the save but must not
  // close whatever dialog is open now. A test answer counts only while no
  // newer test or edit superseded it.
  const generation = useRef(0); // bumped on every close and on unmount
  const probeSeq = useRef(0); // bumped on every test start, edit and close
  useEffect(
    () => () => {
      generation.current++;
    },
    [],
  );

  const editing = edit !== null;
  const problem = draftProblem(draft, editing, editing ? [] : existing);
  const apps = usedBy ?? edit?.apps ?? [];
  const local = draft.type === "local";
  const copy = COPY[draft.type];
  // An edit that changes nothing has nothing to save (nor apps to redeploy).
  const dirty = !editing || JSON.stringify(draft) !== JSON.stringify(initial);

  // A problem shows under its field once the user touched that field or
  // moved past it to a later one — never for a field not reached yet.
  const shown = (field: DraftField) => problem?.field === field && reached >= DRAFT_FIELDS.indexOf(field);
  const hidden = problem !== null && !shown(problem.field);
  const touch = (field: DraftField) => setReached((r) => Math.max(r, DRAFT_FIELDS.indexOf(field)));

  // A problem the user hasn't reached yet (e.g. a secret never focused):
  // Save and Test reveal every problem and go to it, sending nothing.
  const reveal = () => {
    if (!problem) return false;
    setReached(DRAFT_FIELDS.length - 1);
    document.getElementById(fieldId(problem.field))?.focus();
    return true;
  };

  // Any change makes the last test result and save failure stale.
  const set = (patch: Partial<StorageDraft>, field?: DraftField) => {
    probeSeq.current++;
    setDraft((d) => ({ ...d, ...patch }));
    setProbe({ state: "idle" });
    setSaveError("");
    if (field) touch(field);
  };

  const changeOpen = (next: boolean) => {
    if (!next) {
      generation.current++;
      probeSeq.current++;
      setDraft((d) => ({ ...d, secret: "" }));
      setProbe({ state: "idle" });
      setSaveError("");
    }
    onOpenChange(next);
  };

  const test = async () => {
    const seq = ++probeSeq.current;
    const current = () => seq === probeSeq.current;
    setProbe({ state: "testing" });
    try {
      const res = await docker.testStorage(draft);
      if (current()) setProbe(res.ok ? { state: "ok" } : { state: "failed", error: res.error || "The test failed." });
    } catch (e) {
      if (current()) setProbe({ state: "failed", error: errorOf(e) || "The test failed." });
    }
  };

  const save = async () => {
    const opened = generation.current;
    const stillOpen = () => opened === generation.current;
    setBusy(true);
    setSaveError("");
    try {
      const saved = await docker.saveStorage(draft, !editing);
      if (stillOpen()) changeOpen(false);
      onSaved(saved);
    } catch (e) {
      const title = editing ? "Couldn't save storage" : "Couldn't add storage";
      const reason = errorOf(e) || "The save failed.";
      // Open: the reason stays next to the form; closed: only the toast
      // can carry it.
      if (stillOpen()) {
        setSaveError(reason);
        toast.error(title);
      } else {
        toast.error(title, { description: reason });
      }
    } finally {
      setBusy(false);
    }
  };

  const field = (
    key: DraftField,
    label: string,
    value: string,
    onChange: (v: string) => void,
    props: { placeholder?: string; hint?: string; type?: string } = {},
  ) => {
    const id = fieldId(key);
    const invalid = shown(key);
    const describedBy = [props.hint ? `${id}-hint` : "", invalid ? `${id}-error` : ""].filter(Boolean).join(" ");
    return (
      <div className="grid gap-1.5">
        <Label htmlFor={id}>{label}</Label>
        <Input
          id={id}
          type={props.type ?? "text"}
          autoComplete="off"
          spellCheck={false}
          placeholder={props.placeholder}
          aria-invalid={invalid || undefined}
          aria-describedby={describedBy || undefined}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          onBlur={() => touch(key)}
        />
        {props.hint ? (
          <p id={`${id}-hint`} className="text-xs text-muted-foreground">
            {props.hint}
          </p>
        ) : null}
        {invalid ? (
          <p id={`${id}-error`} className="text-xs text-destructive">
            {problem?.message}
          </p>
        ) : null}
      </div>
    );
  };

  const name = draft.name.trim();

  return (
    <Dialog open={open} onOpenChange={changeOpen}>
      <DialogContent
        className="max-h-[calc(100dvh-2rem)] overflow-y-auto sm:max-w-xl"
        onOpenAutoFocus={(e) => {
          // Add: the name comes first. Edit: the caret goes to the end of
          // the first field, its text not selected (one stray key would
          // replace it).
          const first = document.getElementById(fieldId(editing ? (local ? "path" : "endpoint") : "name"));
          if (!(first instanceof HTMLInputElement)) return;
          e.preventDefault();
          first.focus();
          first.setSelectionRange(first.value.length, first.value.length);
        }}
        onCloseAutoFocus={returnFocusTo(returnFocus)}
      >
        <form
          className="grid gap-4"
          onSubmit={(e) => {
            e.preventDefault();
            if (busy || !dirty || reveal()) return;
            void save();
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

          {/* Disabled while saving: what is shown is what is being saved. */}
          <fieldset disabled={busy} className="grid min-w-0 gap-4">
            {editing ? null : (
              <>
                <div className="grid gap-1.5">
                  <Label id="st-type-label">Type</Label>
                  <RadioGroup
                    aria-labelledby="st-type-label"
                    className="flex flex-wrap gap-x-6 gap-y-2"
                    value={draft.type}
                    onValueChange={(v) => set({ type: v as StorageType })}
                  >
                    {(Object.keys(STORAGE_TYPES) as StorageType[]).map((t) => (
                      <div key={t} className="flex items-center gap-2">
                        <RadioGroupItem value={t} id={`st-type-${t}`} />
                        <Label htmlFor={`st-type-${t}`} className="font-normal">
                          {STORAGE_TYPES[t]}
                        </Label>
                      </div>
                    ))}
                  </RadioGroup>
                </div>
                {field("name", "Name", draft.name, (v) => set({ name: v.toLowerCase() }, "name"), {
                  placeholder: copy.namePlaceholder,
                  hint: name
                    ? `Compose files use it as \${${storageVar(name, "…")}}.`
                    : "Lowercase letters, digits and hyphens.",
                })}
              </>
            )}

            {local ? (
              field("path", "Folder", draft.path, (v) => set({ path: v }, "path"), {
                placeholder: "/mnt/backup/winterflow",
                hint: "An existing folder on the disk (under /mnt or /media), not its mount point.",
              })
            ) : (
              <>
                {field(
                  "endpoint",
                  "Endpoint",
                  draft.endpoint,
                  (v) =>
                    // An IP address or localhost has no bucket.host names: on
                    // an add, path-style follows the endpoint while it still
                    // has the endpoint's default (the user didn't set it).
                    set(
                      !editing && draft.pathStyle === pathStyleDefault(draft.endpoint)
                        ? { endpoint: v, pathStyle: pathStyleDefault(v) }
                        : { endpoint: v },
                      "endpoint",
                    ),
                  { placeholder: "https://s3.example.com" },
                )}
                <div className="grid gap-4 sm:grid-cols-2">
                  {field("region", "Region", draft.region, (v) => set({ region: v }, "region"), { placeholder: "e.g. auto" })}
                  {field("bucket", "Bucket", draft.bucket, (v) => set({ bucket: v }, "bucket"))}
                </div>
                {field("prefix", "Prefix", draft.prefix, (v) => set({ prefix: v }, "prefix"), {
                  placeholder: "e.g. winterflow/",
                  hint: "Optional: everything is written under this folder of the bucket.",
                })}
                {field("accessKeyId", "Access key", draft.accessKeyId, (v) => set({ accessKeyId: v }, "accessKeyId"))}
                {field("secret", "Secret key", draft.secret, (v) => set({ secret: v }, "secret"), {
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

            {editing && apps.length > 0 ? (
              <p className="rounded-md bg-amber-500/10 px-3 py-2 text-sm text-amber-800 dark:text-amber-300">
                {/* The agent redeploys only the running ones (and skips an
                    app with undeployed changes, with a warning). */}
                {apps.length === 1
                  ? `Saving redeploys the app that uses it if it's running: ${apps[0]}.`
                  : `Saving redeploys the apps that use it if they're running: ${apps.join(", ")}.`}
              </p>
            ) : null}

            <div className="flex min-h-9 flex-wrap items-center gap-3">
              {/* Enabled while a problem is still hidden, like Save. */}
              <Button
                type="button"
                variant="outline"
                disabled={(problem !== null && !hidden) || probe.state === "testing"}
                onClick={() => {
                  if (!reveal()) void test();
                }}
              >
                {probe.state === "testing" ? copy.testing : copy.test}
              </Button>
              {/* Only the result is announced, not the button's label. */}
              <span role="status" className="flex min-w-0">
                {probe.state === "ok" ? (
                  <span className="flex items-center gap-1.5 text-sm text-emerald-700 dark:text-emerald-400">
                    <CircleCheck className="size-4" /> {copy.ok}
                  </span>
                ) : probe.state === "failed" ? (
                  <span className="flex items-start gap-1.5 text-sm text-destructive [overflow-wrap:anywhere]">
                    <CircleX className="mt-0.5 size-4 shrink-0" /> {probe.error}
                  </span>
                ) : null}
              </span>
            </div>
          </fieldset>

          {busy ? (
            <p role="status" className="text-sm text-muted-foreground">
              {apps.length > 0
                ? "Testing the storage and redeploying apps that use it…"
                : "Testing the storage and saving it…"}{" "}
              You can close this dialog: the save goes on.
            </p>
          ) : null}
          {saveError ? (
            <p
              role="alert"
              className="flex items-start gap-1.5 rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive [overflow-wrap:anywhere]"
            >
              <CircleX className="mt-0.5 size-4 shrink-0" /> {saveError}
            </p>
          ) : null}

          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => changeOpen(false)}>
              Cancel
            </Button>
            {/* Enabled while a problem is still hidden, so Save can say
                what is missing; disabled once the problem is on screen. */}
            <Button type="submit" disabled={(problem !== null && !hidden) || busy || !dirty}>
              {busy ? "Saving…" : "Save"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
