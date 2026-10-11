import { useState, type RefObject } from "react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import { Textarea } from "@/components/ui/textarea";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import type { Docker, DomainCreated } from "@/context/use-docker";
import { domainNameError, TLS_MODES, type Domain, type DomainTLS } from "@/lib/domains";
import { cn } from "@/lib/utils";

// The three ways a registered domain can do HTTPS (see TLS_MODES for the
// Cloudflare setup each one fits).
const MODES = Object.keys(TLS_MODES) as DomainTLS[];

// DomainDialog adds a registration, or (replace set) swaps the TLS mode or
// certificate of an existing one — the agent replaces a name on create. The
// parent remounts it (key) per opening, so state starts from replace. It
// reports a failure itself; onSaved gets the stored registration and the
// proxy's warnings. returnFocus is where focus goes back to on close (the
// dialog has no trigger of its own).
export function DomainDialog({
  docker,
  open,
  replace,
  returnFocus,
  onOpenChange,
  onSaved,
}: {
  docker: Docker;
  open: boolean;
  replace: Domain | null;
  returnFocus?: RefObject<HTMLElement | null>;
  onOpenChange: (open: boolean) => void;
  onSaved: (created: DomainCreated) => void;
}) {
  const [name, setName] = useState(replace?.name ?? "");
  const [tls, setTLS] = useState<DomainTLS>(replace?.tls ?? "auto");
  const [certificate, setCertificate] = useState("");
  const [key, setKey] = useState("");
  const [busy, setBusy] = useState(false);

  const trimmed = name.trim();
  const nameError = trimmed ? domainNameError(trimmed) : "";
  const needsPair = tls === "certificate";
  const canSave =
    trimmed !== "" &&
    !nameError &&
    !busy &&
    (!needsPair || (certificate.trim() !== "" && key.trim() !== ""));

  // The PEM material must not outlive the dialog: wipe it on every close
  // (save, Cancel, Escape, overlay).
  const changeOpen = (next: boolean) => {
    if (!next) {
      setCertificate("");
      setKey("");
    }
    onOpenChange(next);
  };

  const save = async () => {
    setBusy(true);
    try {
      const created = await docker.createDomain(
        trimmed,
        tls,
        needsPair ? { certificate: certificate.trim(), key: key.trim() } : undefined,
      );
      changeOpen(false);
      onSaved(created);
    } catch (e) {
      toast.error(replace ? "Couldn't update domain" : "Couldn't add domain", {
        description: e instanceof Error ? e.message : undefined,
      });
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={changeOpen}>
      {/* Certificate mode is taller than a phone screen: scroll inside. */}
      <DialogContent
        className="max-h-[calc(100dvh-2rem)] overflow-y-auto sm:max-w-xl"
        onCloseAutoFocus={(e) => {
          const back = returnFocus?.current;
          if (back?.isConnected) {
            e.preventDefault();
            back.focus();
          }
        }}
      >
        <form
          className="grid gap-4"
          onSubmit={(e) => {
            e.preventDefault();
            if (canSave) void save();
          }}
        >
          <DialogHeader>
            <DialogTitle>{replace ? "Edit domain" : "Add domain"}</DialogTitle>
            <DialogDescription>
              {replace ? (
                <>
                  Choose how HTTPS works for{" "}
                  <code className="font-mono [overflow-wrap:anywhere] text-foreground">{replace.name}</code>.
                </>
              ) : (
                "Apps on this server can only use hostnames under a domain you've added."
              )}
            </DialogDescription>
          </DialogHeader>
          {/* The name is the registration's identity: editing changes only
              its mode, so an existing one is shown, not offered as a field. */}
          {replace ? null : (
            <div className="grid gap-1.5">
              <Label htmlFor="dom-name">Name</Label>
              <Input
                id="dom-name"
                placeholder="*.example.com"
                autoComplete="off"
                spellCheck={false}
                className={cn("font-mono", nameError && "border-destructive focus-visible:ring-destructive")}
                aria-invalid={nameError !== ""}
                aria-describedby="dom-name-hint"
                value={name}
                onChange={(e) => setName(e.target.value.toLowerCase())}
              />
              <p
                id="dom-name-hint"
                className={cn("text-xs", nameError ? "text-destructive" : "text-muted-foreground")}
              >
                {nameError ||
                  "app.example.com for one hostname, or *.example.com for any name directly under it."}
              </p>
            </div>
          )}
          <div className="grid gap-2">
            <Label id="dom-mode-label">HTTPS</Label>
            <RadioGroup
              aria-labelledby="dom-mode-label"
              value={tls}
              onValueChange={(v) => setTLS(v as DomainTLS)}
              className="gap-3"
            >
              {MODES.map((mode) => (
                <label key={mode} className="flex cursor-pointer items-start gap-2.5 text-sm">
                  <RadioGroupItem
                    value={mode}
                    aria-label={TLS_MODES[mode].label}
                    aria-describedby={`dom-mode-${mode}`}
                    className="mt-0.5"
                  />
                  <span>
                    <span className="font-medium">{TLS_MODES[mode].label}</span>
                    <span id={`dom-mode-${mode}`} className="block text-xs text-muted-foreground">
                      {TLS_MODES[mode].hint}
                    </span>
                  </span>
                </label>
              ))}
            </RadioGroup>
          </div>
          {needsPair ? (
            <>
              <div className="grid gap-1.5">
                <Label htmlFor="dom-cert">Certificate (PEM)</Label>
                <Textarea
                  id="dom-cert"
                  rows={4}
                  spellCheck={false}
                  className="font-mono text-xs"
                  placeholder="-----BEGIN CERTIFICATE-----"
                  aria-describedby="dom-cert-hint"
                  value={certificate}
                  onChange={(e) => setCertificate(e.target.value)}
                />
                <p id="dom-cert-hint" className="text-xs text-muted-foreground">
                  {replace?.tls === "certificate"
                    ? "Paste the certificate again, or a new one. "
                    : null}
                  It also serves the Auto TLS hostnames it covers, instead of Let's Encrypt.
                </p>
              </div>
              <div className="grid gap-1.5">
                <Label htmlFor="dom-key">Private key (PEM)</Label>
                <Textarea
                  id="dom-key"
                  rows={4}
                  spellCheck={false}
                  className="font-mono text-xs"
                  placeholder="-----BEGIN PRIVATE KEY-----"
                  aria-describedby="dom-key-hint"
                  value={key}
                  onChange={(e) => setKey(e.target.value)}
                />
                <p id="dom-key-hint" className="text-xs text-muted-foreground">
                  Encrypted in your browser; only this server can read it.
                </p>
              </div>
            </>
          ) : null}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => changeOpen(false)}>
              Cancel
            </Button>
            <Button type="submit" disabled={!canSave}>
              {busy ? "Saving…" : replace ? "Save" : "Add"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
