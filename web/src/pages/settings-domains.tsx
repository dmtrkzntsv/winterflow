import { useCallback, useEffect, useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { Pencil, Plus, RefreshCw, Trash2 } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { buttonVariants } from "@/components/ui/button-variants";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import { Spinner } from "@/components/ui/spinner";
import { Textarea } from "@/components/ui/textarea";
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
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { TLSBadge } from "@/components/tls-badge";
import { type Docker } from "@/context/use-docker";
import { useApps } from "@/context/use-apps";
import {
  certExpiry,
  daysLeft,
  domainNameError,
  TLS_LABELS,
  type DomainTLS,
  type RegisteredDomain,
} from "@/lib/domains";
import { cn } from "@/lib/utils";

// The three ways a registered domain can do HTTPS, in plain words with the
// Cloudflare setup each one fits.
const MODES: { value: DomainTLS; hint: string }[] = [
  {
    value: "auto",
    hint: "Gets a free Let's Encrypt certificate for each app hostname. For a DNS record pointing at this server (on Cloudflare: DNS only, grey cloud).",
  },
  {
    value: "certificate",
    hint: "Uses a certificate you paste in, such as a Cloudflare Origin certificate behind the Cloudflare proxy.",
  },
  {
    value: "off",
    hint: "Serves plain HTTP, for when something in front handles HTTPS, such as a Cloudflare Tunnel.",
  },
];

// Expiry shows how long a certificate-mode domain's certificate has left:
// a countdown once it's under 30 days (amber), a date otherwise, red once
// past. Other modes renew themselves or have no certificate here.
function Expiry({ domain }: { domain: RegisteredDomain }) {
  const grade = certExpiry(domain.not_after);
  if (domain.tls !== "certificate" || !grade || !domain.not_after) return null;
  const day = domain.not_after.slice(0, 10);
  const days = daysLeft(domain.not_after) ?? 0;
  const text =
    grade === "expired"
      ? `Expired ${day}`
      : grade === "soon"
        ? `Expires in ${days === 1 ? "1 day" : `${days} days`}`
        : `Expires ${day}`;
  const title = [grade === "soon" ? day : "", domain.issuer ? `Issued by ${domain.issuer}` : ""]
    .filter(Boolean)
    .join(" · ");
  return (
    <span
      title={title || undefined}
      className={cn(
        "whitespace-nowrap",
        grade === "soon" && "font-medium text-amber-700 dark:text-amber-400",
        grade === "expired" && "font-medium text-destructive",
      )}
    >
      {text}
    </span>
  );
}

function UsedBy({ ids, names }: { ids: string[]; names: Map<string, string> }) {
  if (ids.length === 0) return <span className="text-muted-foreground">Not used</span>;
  return (
    <span>
      {ids.length === 1 ? "1 app: " : `${ids.length} apps: `}
      {ids.map((id, i) => (
        <span key={id}>
          {i > 0 && ", "}
          <Link to={`/app/${id}`} className="font-medium underline-offset-4 hover:underline">
            {names.get(id) ?? id}
          </Link>
        </span>
      ))}
    </span>
  );
}

// toastResult reports a domain mutation: success, or a warning when the proxy
// reload that applied it reported problems (the change itself is stored).
function toastResult(done: string, warnings: string[]) {
  if (warnings.length > 0) {
    toast.warning(`${done}, but the proxy reported a problem`, {
      description: warnings.join("\n"),
    });
  } else {
    toast.success(done);
  }
}

// DomainDialog adds a registration, or (replace set) swaps the TLS mode or
// certificate of an existing one — the agent replaces a name on create. The
// parent remounts it (key) per opening, so state starts from replace.
export function DomainDialog({
  docker,
  open,
  replace,
  onOpenChange,
  onSaved,
}: {
  docker: Docker;
  open: boolean;
  replace: RegisteredDomain | null;
  onOpenChange: (open: boolean) => void;
  onSaved: () => Promise<void>;
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
      const warnings = await docker.createDomain(
        trimmed,
        tls,
        needsPair ? { certificate: certificate.trim(), key: key.trim() } : undefined,
      );
      toastResult(replace ? "Domain updated" : "Domain registered", warnings);
      changeOpen(false);
      await onSaved();
    } catch (e) {
      toast.error(replace ? "Couldn't update domain" : "Couldn't register domain", {
        description: e instanceof Error ? e.message : undefined,
      });
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={changeOpen}>
      {/* Certificate mode is taller than a phone screen: scroll inside. */}
      <DialogContent className="max-h-[calc(100dvh-2rem)] overflow-y-auto sm:max-w-xl">
        <form
          className="grid gap-4"
          onSubmit={(e) => {
            e.preventDefault();
            if (canSave) void save();
          }}
        >
          <DialogHeader>
            <DialogTitle>{replace ? "Edit domain" : "Register domain"}</DialogTitle>
            <DialogDescription>
              {replace ? (
                <>
                  Choose how HTTPS works for{" "}
                  <code className="font-mono [overflow-wrap:anywhere] text-foreground">{replace.name}</code>.
                </>
              ) : (
                "Apps on this server can only use hostnames under a registered domain."
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
              {MODES.map((m) => (
                <label key={m.value} className="flex cursor-pointer items-start gap-2.5 text-sm">
                  <RadioGroupItem
                    value={m.value}
                    aria-label={TLS_LABELS[m.value]}
                    aria-describedby={`dom-mode-${m.value}`}
                    className="mt-0.5"
                  />
                  <span>
                    <span className="font-medium">{TLS_LABELS[m.value]}</span>
                    <span id={`dom-mode-${m.value}`} className="block text-xs text-muted-foreground">
                      {m.hint}
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
              {busy ? "Saving…" : replace ? "Save" : "Register"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

export function DomainsCard({ docker }: { docker: Docker }) {
  const { apps } = useApps();
  const [items, setItems] = useState<RegisteredDomain[] | null>(null);
  const [loading, setLoading] = useState(true);
  const [dialogOpen, setDialogOpen] = useState(false);
  const [replace, setReplace] = useState<RegisteredDomain | null>(null);
  const [dialogKey, setDialogKey] = useState(0);
  const [toDelete, setToDelete] = useState<RegisteredDomain | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      setItems(await docker.listDomains());
    } catch (e) {
      toast.error("Failed to load domains", {
        description: e instanceof Error ? e.message : undefined,
      });
    } finally {
      setLoading(false);
    }
  }, [docker]);

  useEffect(() => {
    void load();
  }, [load]);

  const remove = async (name: string) => {
    try {
      toastResult("Domain removed", await docker.deleteDomain(name));
    } catch (e) {
      // A host only this name covers: the agent's message names the apps.
      toast.error("Couldn't remove domain", {
        description: e instanceof Error ? e.message : undefined,
      });
    }
    await load();
  };

  const names = useMemo(() => new Map(apps.map((a) => [a.id, a.name])), [apps]);
  const openDialog = (row: RegisteredDomain | null) => {
    setReplace(row);
    setDialogKey((k) => k + 1);
    setDialogOpen(true);
  };

  const deleteApps = toDelete?.apps ?? [];

  return (
    <Card>
      <CardHeader className="flex-row flex-wrap items-start justify-between gap-x-4 gap-y-3 space-y-0">
        <div className="min-w-0 space-y-1.5">
          <CardTitle>Domains</CardTitle>
          <CardDescription>
            Apps on this server can use hostnames under these domains. Each one
            sets how HTTPS works.
          </CardDescription>
        </div>
        <div className="flex items-center gap-2">
          <Button
            size="icon"
            variant="ghost"
            className="size-8 text-muted-foreground"
            aria-label="Refresh domains"
            title="Refresh domains"
            onClick={() => void load()}
            disabled={loading}
          >
            <RefreshCw className={cn("size-4", loading && "animate-spin")} />
          </Button>
          <Button size="sm" onClick={() => openDialog(null)}>
            <Plus className="size-4" /> Register domain
          </Button>
        </div>
      </CardHeader>
      <CardContent>
        {items === null ? (
          <div className="flex h-24 items-center justify-center">
            {loading ? <Spinner /> : <p className="text-sm text-muted-foreground">Couldn't load domains.</p>}
          </div>
        ) : items.length === 0 ? (
          <p className="rounded-lg border border-dashed px-4 py-10 text-center text-sm text-muted-foreground">
            No domains yet. Register one, e.g. <code className="font-mono">*.example.com</code>, before
            giving an app a hostname.
          </p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>HTTPS</TableHead>
                <TableHead className="hidden sm:table-cell">Certificate</TableHead>
                <TableHead className="hidden sm:table-cell">Used by</TableHead>
                <TableHead className="w-20">
                  <span className="sr-only">Actions</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.map((d) => {
                const used = d.apps ?? [];
                return (
                  <TableRow key={d.name}>
                    {/* Names wrap rather than truncate: the suffix is the
                        part that matters. */}
                    <TableCell>
                      <div className="font-mono [overflow-wrap:anywhere]">{d.name}</div>
                      {/* Phones drop the Certificate and Used by columns;
                          keep what needs attention visible inline. */}
                      {d.tls === "certificate" || used.length > 0 ? (
                        <div className="mt-1 flex flex-wrap gap-x-2 text-xs text-muted-foreground sm:hidden">
                          <Expiry domain={d} />
                          {used.length > 0 ? (
                            <span>{used.length === 1 ? "1 app" : `${used.length} apps`}</span>
                          ) : null}
                        </div>
                      ) : null}
                    </TableCell>
                    <TableCell>
                      <TLSBadge tls={d.tls} />
                    </TableCell>
                    <TableCell className="hidden sm:table-cell">
                      {d.tls === "certificate" ? (
                        <Expiry domain={d} />
                      ) : (
                        <span className="text-muted-foreground">—</span>
                      )}
                    </TableCell>
                    <TableCell className="hidden sm:table-cell">
                      <UsedBy ids={used} names={names} />
                    </TableCell>
                    <TableCell className="whitespace-nowrap py-0.5 text-right">
                      <Button
                        size="icon"
                        variant="ghost"
                        className="size-8 text-muted-foreground"
                        aria-label={`Edit domain ${d.name}`}
                        title={d.tls === "certificate" ? "Change HTTPS mode or replace certificate" : "Change HTTPS mode"}
                        onClick={() => openDialog(d)}
                      >
                        <Pencil className="size-4" />
                      </Button>
                      <Button
                        size="icon"
                        variant="ghost"
                        className="size-8 text-muted-foreground hover:text-destructive"
                        aria-label={`Remove domain ${d.name}`}
                        title="Remove domain"
                        onClick={() => setToDelete(d)}
                      >
                        <Trash2 className="size-4" />
                      </Button>
                    </TableCell>
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        )}
      </CardContent>
      <DomainDialog
        key={dialogKey}
        docker={docker}
        open={dialogOpen}
        replace={replace}
        onOpenChange={setDialogOpen}
        onSaved={load}
      />
      <AlertDialog open={toDelete !== null} onOpenChange={(o) => !o && setToDelete(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Remove this domain?</AlertDialogTitle>
            <AlertDialogDescription>
              Apps on this server will no longer be able to use{" "}
              <code className="font-mono [overflow-wrap:anywhere] text-foreground">{toDelete?.name}</code>.
              {deleteApps.length > 0 ? (
                <>
                  {" "}
                  It's used by{" "}
                  <span className="font-medium text-foreground">
                    {deleteApps.map((id) => names.get(id) ?? id).join(", ")}
                  </span>
                  : remove those hostnames first, unless another registered domain covers them.
                </>
              ) : null}
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
              Remove domain
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </Card>
  );
}
