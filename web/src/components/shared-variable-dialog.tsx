import { useEffect, useState } from "react";
import { toast } from "sonner";

import { Disclosure } from "@/components/disclosure";
import { ResponsiveDialog } from "@/components/responsive-dialog";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
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
  type VariableScope,
} from "@/lib/shared-variables";

export type DialogMode =
  | { kind: "create" }
  | { kind: "edit"; variable: SharedVariable }
  | { kind: "provide"; variable: SharedVariable };

const FORM_ID = "shared-variable-form";

// SharedVariableDialog adds or edits a shared variable, or provides a
// missing secret for the servers that lack it (gap fill). A new variable
// applies to all servers unless chooseScope offers "{server} only" (under
// Advanced) and the user picks it; an edit or a provide keeps the
// variable's scope. A server-only secret is encrypted for that server's key
// only, an org one for every keyed server, a provide for the keyed servers
// missing it. An existing secret's name, kind and value are locked:
// "Replace value" sends a new value; anything else is delete and recreate.
// Secret values are encrypted here and cleared on close. The parent
// remounts it (key) per opening; onSave reports nothing itself (it throws
// to keep the dialog open).
export function SharedVariableDialog({
  open,
  mode,
  keys,
  server = null,
  chooseScope = false,
  onOpenChange,
  onSave,
  onCloseAutoFocus,
}: {
  open: boolean;
  mode: DialogMode;
  keys: EncryptionKeys;
  // server is the active server: a "{name} only" variable belongs to it.
  server?: { id: string; name: string } | null;
  // chooseScope offers "Applies to" on a new variable.
  chooseScope?: boolean;
  onOpenChange: (open: boolean) => void;
  onSave: (body: SaveVariable, scope: VariableScope) => Promise<void>;
  // onCloseAutoFocus is where focus goes once it closes (the opener may be gone).
  onCloseAutoFocus?: (event: Event) => void;
}) {
  const existing = mode.kind === "create" ? null : mode.variable;
  const provide = mode.kind === "provide";
  const [scope, setScope] = useState<VariableScope>(existing?.scope ?? "org");
  const [name, setName] = useState(existing?.name ?? "");
  const [secret, setSecret] = useState(existing?.is_secret ?? false);
  const [value, setValue] = useState(existing && !existing.is_secret ? existing.value : "");
  const [description, setDescription] = useState(existing?.description ?? "");
  const [replace, setReplace] = useState(false);
  const [reload, setReload] = useState(false);
  const [busy, setBusy] = useState(false);

  const serverId = scope === "server" ? server?.id : undefined;
  const names = serverNameMap(keys);
  const scopeKeys = serverId ? keys.servers.filter((k) => k.server_id === serverId) : keys.servers;
  const pending = serverId ? keys.pending_servers.filter((p) => p.server_id === serverId) : keys.pending_servers;
  const typingSecret = secret && (mode.kind === "create" || provide || replace);
  const lockedSecret = mode.kind === "edit" && secret && !replace;
  const trimmed = name.trim();
  const nameError = mode.kind === "create" && trimmed ? variableNameError(trimmed) : "";
  // Who actually receives the new value: keyed servers only.
  const targets = provide ? keys.servers.filter((k) => existing?.missing_on.includes(k.server_id)) : scopeKeys;
  const keyless = provide ? (existing?.missing_on ?? []).filter((id) => !targets.some((k) => k.server_id === id)) : [];
  const noKey = typingSecret && targets.length === 0;
  const canSave = !busy && trimmed !== "" && !nameError && !noKey && (!typingSecret || value !== "");
  const pushesEverywhere = typingSecret && !provide && serverId === undefined;

  // The typed value must not outlive the dialog.
  useEffect(() => {
    if (!open) setValue("");
  }, [open]);

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
      } else if (typingSecret) {
        body = { ...common, is_secret: true, ciphertexts: await encryptForServers(value, targets) };
      } else {
        body = { ...common, is_secret: true };
      }
      await onSave(body, scope);
      changeOpen(false);
    } catch (e) {
      toast.error("Couldn't save the variable", { description: e instanceof Error ? e.message : undefined });
    } finally {
      setBusy(false);
    }
  };

  const title = provide ? `Provide ${existing?.name}` : existing ? `Edit ${existing.name}` : "Add variable";
  const label = (id: string) => names.get(id) ?? id;
  const targetNames = targets.map((k) => k.name).join(", ");
  const keylessNames = keyless.map(label).join(", ");
  const scopeText = scope === "server" && server ? `${server.name} only` : "all servers";

  return (
    <ResponsiveDialog
      open={open}
      onOpenChange={changeOpen}
      onCloseAutoFocus={onCloseAutoFocus}
      title={title}
      description={
        provide
          ? `Encrypts the value for: ${targetNames || "no server"}${keylessNames ? `. Pending (no key yet): ${keylessNames}` : ""}`
          : "Apps use it as ${NAME}. A secret is encrypted in your browser, once per server."
      }
      footer={
        <>
          <Button type="button" variant="outline" className="max-sm:h-10" onClick={() => changeOpen(false)}>
            Cancel
          </Button>
          <Button type="submit" form={FORM_ID} className="max-sm:h-10" disabled={!canSave}>
            {pushesEverywhere ? "Save and push" : "Save"}
          </Button>
        </>
      }
    >
      <form
        id={FORM_ID}
        className="space-y-4"
        onSubmit={(e) => {
          e.preventDefault();
          if (canSave) void save();
        }}
      >
        <div className="space-y-2">
          <Label htmlFor="sv-name">Name</Label>
          <Input
            id="sv-name"
            value={name}
            disabled={existing !== null}
            placeholder="SMTP_PASS"
            className="font-mono"
            autoComplete="off"
            spellCheck={false}
            aria-invalid={nameError ? true : undefined}
            aria-describedby={nameError ? "sv-name-error" : undefined}
            onChange={(e) => setName(e.target.value)}
          />
          {nameError ? (
            <p id="sv-name-error" className="text-sm text-destructive">
              {nameError}
            </p>
          ) : null}
        </div>
        {lockedSecret ? (
          <div className="flex flex-wrap items-center justify-between gap-2 rounded-md border p-3 text-sm">
            <span className="text-muted-foreground">The value is encrypted and can't be shown.</span>
            <Button type="button" size="sm" variant="outline" className="max-sm:h-10" onClick={() => setReplace(true)}>
              Replace value
            </Button>
          </div>
        ) : (
          <div className="space-y-2">
            <Label htmlFor="sv-value">Value</Label>
            <Textarea
              id="sv-value"
              rows={3}
              value={value}
              className="font-mono"
              autoComplete="off"
              spellCheck={false}
              onChange={(e) => setValue(e.target.value)}
            />
            {typingSecret && !provide ? (
              <p className="text-sm text-muted-foreground">
                {noKey
                  ? serverId !== undefined
                    ? "This server has no public key yet, so it can't receive secrets."
                    : "No server has a public key yet, so there is nobody to receive this secret."
                  : pushSummary(scopeKeys.length, pending.length)}
              </p>
            ) : null}
          </div>
        )}
        {provide ? null : (
          <div className="flex items-center gap-2">
            <Switch id="sv-secret" checked={secret} disabled={existing !== null} onCheckedChange={setSecret} />
            <Label htmlFor="sv-secret">Secret</Label>
          </div>
        )}
        <div className="flex items-center gap-2">
          <Checkbox id="sv-reload" checked={reload} onCheckedChange={(c) => setReload(c === true)} />
          <Label htmlFor="sv-reload">Reload apps that use it now</Label>
        </div>
        {provide ? null : (
          <Disclosure label="Advanced" defaultOpen={Boolean(existing?.description)}>
            <div className="space-y-2">
              <Label htmlFor="sv-description">Description</Label>
              <Input id="sv-description" value={description} onChange={(e) => setDescription(e.target.value)} />
            </div>
            {mode.kind === "create" && chooseScope && server ? (
              <div className="space-y-2">
                <Label id="sv-scope-label">Applies to</Label>
                <RadioGroup
                  aria-labelledby="sv-scope-label"
                  value={scope}
                  onValueChange={(v) => setScope(v as VariableScope)}
                  className="gap-2"
                >
                  <label className="flex cursor-pointer items-center gap-2 text-sm">
                    <RadioGroupItem value="org" aria-label="All servers" />
                    All servers
                  </label>
                  <label className="flex cursor-pointer items-center gap-2 text-sm">
                    <RadioGroupItem value="server" aria-label={`${server.name} only`} />
                    {server.name} only
                  </label>
                </RadioGroup>
              </div>
            ) : existing ? (
              <p className="text-sm text-muted-foreground">Applies to {scopeText}.</p>
            ) : null}
          </Disclosure>
        )}
      </form>
    </ResponsiveDialog>
  );
}
