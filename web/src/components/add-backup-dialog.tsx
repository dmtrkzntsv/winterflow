import { useState, type RefObject } from "react";

import { Button } from "@/components/ui/button";
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
import { InlineError } from "@/components/inline-error";
import { NativeSelect } from "@/components/native-select";
import { PathsField, ScheduleSelect } from "@/components/backup-fields";
import {
  backupNameError,
  everyError,
  locationError,
  pathsFrom,
  type BackupConfig,
  type BackupTargets,
} from "@/lib/backups";
import { returnFocusTo } from "@/lib/focus";

// A place is "volume:<key>" or "mount:<service>:<target>".
const volumePlace = (v: string) => `volume:${v}`;
const mountPlace = (service: string, target: string) => `mount:${service}:${target}`;

// AddBackupDialog adds a sqlite or files backup the compose file doesn't
// declare. The picker lists the compose file's volumes and each service's
// mounts (volumes and bind mounts); the paths come after. The backup is
// added off: its switch picks the storage. taken are the app's backup
// names; error is why the last add was refused, kept in view next to the
// button. A volume is offered once (named, with where it is mounted), not
// again as each service's mount of it. The parent remounts it per opening.
export function AddBackupDialog({
  targets,
  taken,
  open,
  busy,
  error = "",
  returnFocus,
  onOpenChange,
  onAdd,
}: {
  targets: BackupTargets | null;
  taken: readonly string[];
  open: boolean;
  busy: boolean;
  error?: string;
  returnFocus?: RefObject<HTMLElement | null>;
  onOpenChange: (open: boolean) => void;
  onAdd: (entry: BackupConfig) => void;
}) {
  const [name, setName] = useState("");
  const [type, setType] = useState<"sqlite" | "files">("sqlite");
  const [place, setPlace] = useState("");
  const [pathsText, setPathsText] = useState("");
  const [every, setEvery] = useState("1h");
  const [shown, setShown] = useState(false); // the user tried to add

  const volumes = targets?.volumes ?? [];
  // A service's mount of a volume the compose file declares is that volume:
  // the picker names it once, under Volumes.
  const wholeVolume = (m: { type: string; source: string }) => m.type === "volume" && volumes.includes(m.source);
  const services = (targets?.services ?? [])
    .map((s) => ({ ...s, mounts: s.mounts.filter((m) => !wholeVolume(m)) }))
    .filter((s) => s.mounts.length > 0);
  const mountedIn = (v: string) =>
    (targets?.services ?? []).flatMap((s) => s.mounts.filter((m) => m.type === "volume" && m.source === v).map((m) => `${s.name} at ${m.target}`));
  const nothing = volumes.length === 0 && services.length === 0;
  const [kind, key] = place.split(":");
  const where = kind === "volume" ? "volume" : "service";
  const target = kind === "volume" ? key : (key ?? "");
  const paths = pathsFrom(pathsText);
  const problem =
    backupNameError(name.trim(), taken) ||
    (place ? locationError(type, where, target, paths) : "Pick where the data is.") ||
    everyError(every);

  const pick = (next: string) => {
    setPlace(next);
    // A mount's target starts the path in the container.
    const [k, , ...t] = next.split(":");
    if (k === "mount" && !pathsText.trim()) setPathsText(type === "sqlite" ? `${t.join(":")}/` : t.join(":"));
  };

  const add = () => {
    setShown(true);
    if (problem || busy) return;
    const entry: BackupConfig = { name: name.trim(), type, every, [where]: target };
    if (type === "sqlite") entry.path = paths[0];
    else if (paths.length > 0) entry.paths = paths;
    onAdd(entry);
  };

  if (nothing) {
    return (
      <NoPlacesDialog
        service={targets?.services[0]?.name ?? "app"}
        open={open}
        returnFocus={returnFocus}
        onOpenChange={onOpenChange}
      />
    );
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        className="max-h-[calc(100dvh-2rem)] overflow-y-auto sm:max-w-lg"
        onCloseAutoFocus={returnFocusTo(returnFocus)}
      >
        <form
          className="grid gap-4"
          onSubmit={(e) => {
            e.preventDefault();
            add();
          }}
        >
          <DialogHeader>
            <DialogTitle>Add backup</DialogTitle>
            <DialogDescription>
              Back up a database file or folders the compose file doesn't declare. It is added off: turn it on to pick its
              storage.
            </DialogDescription>
          </DialogHeader>
          <fieldset disabled={busy} className="grid min-w-0 gap-4">
            <div className="grid gap-1.5">
              <Label htmlFor="add-backup-name">Name</Label>
              <Input
                id="add-backup-name"
                autoComplete="off"
                placeholder="e.g. uploads"
                value={name}
                onChange={(e) => setName(e.target.value.toLowerCase())}
              />
            </div>
            <div className="grid gap-1.5">
              <Label id="add-backup-type">Type</Label>
              <RadioGroup
                aria-labelledby="add-backup-type"
                className="flex gap-6"
                value={type}
                onValueChange={(v) => setType(v as "sqlite" | "files")}
              >
                {(
                  [
                    ["sqlite", "SQLite database"],
                    ["files", "Folders and files"],
                  ] as const
                ).map(([v, label]) => (
                  <div key={v} className="flex items-center gap-2">
                    <RadioGroupItem value={v} id={`add-backup-${v}`} />
                    <Label htmlFor={`add-backup-${v}`} className="font-normal">
                      {label}
                    </Label>
                  </div>
                ))}
              </RadioGroup>
            </div>
            <div className="grid gap-1.5">
              <Label htmlFor="add-backup-place">Where the data is</Label>
              <NativeSelect id="add-backup-place" value={place} onChange={(e) => pick(e.target.value)}>
                <option value="" disabled>
                  Pick a volume or a mount…
                </option>
                {volumes.length > 0 ? (
                  <optgroup label="Volumes">
                    {volumes.map((v) => (
                      <option key={v} value={volumePlace(v)}>
                        Volume {v}
                        {mountedIn(v).length > 0 ? ` (in ${mountedIn(v).join(", ")})` : ""}
                      </option>
                    ))}
                  </optgroup>
                ) : null}
                {services.map((s) => (
                  <optgroup key={s.name} label={`Service ${s.name}`}>
                    {s.mounts.map((m) => (
                      <option key={m.target} value={mountPlace(s.name, m.target)}>
                        {m.target} ({m.type === "volume" ? `volume ${m.source}` : m.source})
                      </option>
                    ))}
                  </optgroup>
                ))}
              </NativeSelect>
              {kind === "mount" ? (
                <p className="text-xs text-muted-foreground">Paths are in the {key} container.</p>
              ) : null}
            </div>
            <PathsField id="add-backup-paths" type={type} where={where} value={pathsText} onChange={setPathsText} />
            <div className="grid gap-1.5">
              <Label htmlFor="add-backup-every">Schedule</Label>
              <ScheduleSelect id="add-backup-every" value={every} onChange={setEvery} />
            </div>
          </fieldset>
          {error ? (
            <InlineError icon className="[overflow-wrap:anywhere]">
              {error}
            </InlineError>
          ) : null}
          {shown && problem ? <InlineError icon>{problem}</InlineError> : null}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button type="submit" disabled={busy || (shown && !!problem)}>
              {busy ? "Adding…" : "Add backup"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

// NoPlacesDialog stands in for the form when the app's compose file has no
// volume or bind mount: a backup reads data through the app's mounts, so a
// folder that lives only inside a container (its image's /var/log, say)
// can't be reached until the compose file mounts it.
function NoPlacesDialog({
  service,
  open,
  returnFocus,
  onOpenChange,
}: {
  service: string;
  open: boolean;
  returnFocus?: RefObject<HTMLElement | null>;
  onOpenChange: (open: boolean) => void;
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[calc(100dvh-2rem)] overflow-y-auto sm:max-w-lg" onCloseAutoFocus={returnFocusTo(returnFocus)}>
        <DialogHeader>
          <DialogTitle>Add backup</DialogTitle>
          <DialogDescription>This app has nothing a backup can reach yet.</DialogDescription>
        </DialogHeader>
        <div className="grid gap-3 text-sm" role="note">
          <p>
            Backups read data through the app&apos;s volumes and bind mounts, and its compose file has none. Files that
            live only inside a container can&apos;t be reached.
          </p>
          <p>Mount the folder you want to keep in the compose file (Edit the app), then add the backup here. For example:</p>
          <pre className="overflow-x-auto rounded-md border bg-muted/40 p-3 font-mono text-xs leading-relaxed">{`services:
  ${service}:
    volumes:
      - data:/path/in/the/container
volumes:
  data:`}</pre>
          <p className="text-muted-foreground">
            A new named volume starts with the image&apos;s files at that path; a bind mount starts with the host
            folder&apos;s.
          </p>
        </div>
        <DialogFooter>
          <Button type="button" onClick={() => onOpenChange(false)}>
            Got it
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
