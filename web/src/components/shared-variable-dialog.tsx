import { useState } from "react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import {
  encryptForServers,
  pushSummary,
  serverNameMap,
  variableNameError,
  type EncryptionKeys,
  type SaveVariable,
  type SharedVariable,
} from "@/lib/shared-variables";

export type DialogMode =
  | { kind: "create" }
  | { kind: "edit"; variable: SharedVariable }
  | { kind: "provide"; variable: SharedVariable };

// SharedVariableDialog adds or edits a shared variable, or provides a
// missing secret for the servers that lack it (gap fill). serverId set =
// a server-scope variable, whose secret is encrypted for that server only.
// An existing secret's name, kind and value are locked: "Replace value"
// sends a new value to every keyed server; anything else is delete and
// recreate. Secret values are encrypted here and cleared on close. The
// parent remounts it (key) per opening.
export function SharedVariableDialog({
  open,
  mode,
  keys,
  serverId,
  onOpenChange,
  onSave,
}: {
  open: boolean;
  mode: DialogMode;
  keys: EncryptionKeys;
  serverId?: string;
  onOpenChange: (open: boolean) => void;
  onSave: (body: SaveVariable) => Promise<void>;
}) {
  const existing = mode.kind === "create" ? null : mode.variable;
  const provide = mode.kind === "provide";
  const [name, setName] = useState(existing?.name ?? "");
  const [secret, setSecret] = useState(existing?.is_secret ?? false);
  const [value, setValue] = useState(existing && !existing.is_secret ? existing.value : "");
  const [description, setDescription] = useState(existing?.description ?? "");
  const [replace, setReplace] = useState(false);
  const [reload, setReload] = useState(false);
  const [busy, setBusy] = useState(false);

  const names = serverNameMap(keys);
  const scopeKeys = serverId ? keys.servers.filter((k) => k.server_id === serverId) : keys.servers;
  const pending = serverId ? keys.pending_servers.filter((p) => p.server_id === serverId) : keys.pending_servers;
  const typingSecret = secret && (mode.kind === "create" || provide || replace);
  const lockedSecret = mode.kind === "edit" && secret && !replace;
  const trimmed = name.trim();
  const nameError = mode.kind === "create" && trimmed ? variableNameError(trimmed) : "";
  const noKey = typingSecret && !provide && serverId !== undefined && scopeKeys.length === 0;
  const canSave = !busy && trimmed !== "" && !nameError && !noKey && (!typingSecret || value !== "");
  const pushesEverywhere = typingSecret && !provide && serverId === undefined;

  // The typed value must not outlive the dialog.
  const changeOpen = (next: boolean) => {
    if (!next) setValue("");
    onOpenChange(next);
  };

  const save = async () => {
    setBusy(true);
    try {
      const common = { name: trimmed, description, ...(reload ? { reload: true } : {}) };
      let body: SaveVariable;
      if (!secret) {
        body = { ...common, is_secret: false, value };
      } else if (provide && existing) {
        body = { ...common, is_secret: true, ciphertexts: await encryptForServers(value, keys.servers, existing.missing_on) };
      } else if (typingSecret) {
        body = { ...common, is_secret: true, ciphertexts: await encryptForServers(value, scopeKeys) };
      } else {
        body = { ...common, is_secret: true };
      }
      await onSave(body);
      changeOpen(false);
    } catch (e) {
      toast.error("Couldn't save the variable", {
        description: e instanceof Error ? e.message : undefined,
      });
    } finally {
      setBusy(false);
    }
  };

  const title = provide ? `Provide ${existing?.name}` : existing ? `Edit ${existing.name}` : "Add variable";
  const missingNames = existing?.missing_on.map((id) => names.get(id) ?? id).join(", ") ?? "";

  return (
    <Dialog open={open} onOpenChange={changeOpen}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>
            {provide
              ? `Encrypts the value for: ${missingNames}`
              : "Apps use it as ${NAME}. Secret values are encrypted in your browser, once per server."}
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="sv-name">Name</Label>
            <Input
              id="sv-name"
              value={name}
              disabled={existing !== null}
              placeholder="SMTP_PASS"
              className="font-mono"
              onChange={(e) => setName(e.target.value)}
            />
            {nameError ? <p className="text-sm text-destructive">{nameError}</p> : null}
          </div>
          {provide ? null : (
            <div className="flex items-center gap-2">
              <Switch id="sv-secret" checked={secret} disabled={existing !== null} onCheckedChange={setSecret} />
              <Label htmlFor="sv-secret">Secret</Label>
            </div>
          )}
          {lockedSecret ? (
            <div className="flex items-center justify-between gap-2 rounded-md border p-3 text-sm">
              <span className="text-muted-foreground">The value is encrypted and can't be shown.</span>
              <Button size="sm" variant="outline" onClick={() => setReplace(true)}>
                Replace value
              </Button>
            </div>
          ) : (
            <div className="space-y-2">
              <Label htmlFor="sv-value">Value</Label>
              <Textarea id="sv-value" rows={3} value={value} className="font-mono" onChange={(e) => setValue(e.target.value)} />
              {typingSecret && !provide ? (
                <p className="text-sm text-muted-foreground">
                  {noKey ? "This server has no public key yet, so it can't receive secrets." : pushSummary(scopeKeys.length, pending.length)}
                </p>
              ) : null}
            </div>
          )}
          {provide ? null : (
            <div className="space-y-2">
              <Label htmlFor="sv-description">Description</Label>
              <Input id="sv-description" value={description} onChange={(e) => setDescription(e.target.value)} />
            </div>
          )}
          <div className="flex items-center gap-2">
            <Checkbox id="sv-reload" checked={reload} onCheckedChange={(c) => setReload(c === true)} />
            <Label htmlFor="sv-reload">Reload affected apps now</Label>
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => changeOpen(false)}>
            Cancel
          </Button>
          <Button disabled={!canSave} onClick={() => void save()}>
            {pushesEverywhere ? "Save and push" : "Save"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
