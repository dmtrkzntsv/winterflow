import { useCallback, useEffect, useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { Pencil, Plus, RefreshCw, Trash2 } from "lucide-react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
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
import { type Docker } from "@/context/use-docker";
import { useApps } from "@/context/use-apps";
import {
  certExpiry,
  domainNameError,
  isWildcard,
  TLS_LABELS,
  type DomainTLS,
  type RegisteredDomain,
} from "@/lib/domains";
import { cn } from "@/lib/utils";

const MODES: { value: DomainTLS; hint: string }[] = [
  {
    value: "auto",
    hint: "Winterflow gets a Let's Encrypt certificate for each app hostname. Use it with an A record pointing at this server (Cloudflare: DNS only).",
  },
  {
    value: "certificate",
    hint: "Upload a certificate and key, e.g. a Cloudflare Origin certificate behind the Cloudflare proxy.",
  },
  {
    value: "off",
    hint: "TLS ends in front of this server, e.g. a Cloudflare Tunnel. Apps are served over plain HTTP.",
  },
];

function TLSBadge({ tls }: { tls: DomainTLS }) {
  return (
    <Badge variant={tls === "off" ? "outline" : "secondary"} className="whitespace-nowrap">
      {TLS_LABELS[tls]}
    </Badge>
  );
}

function Expiry({ domain }: { domain: RegisteredDomain }) {
  const grade = certExpiry(domain.not_after);
  if (domain.tls !== "certificate" || !grade || !domain.not_after) {
    return <span className="text-muted-foreground">—</span>;
  }
  const day = domain.not_after.slice(0, 10);
  return (
    <span
      title={domain.issuer ? `Issued by ${domain.issuer}` : undefined}
      className={cn(
        "whitespace-nowrap",
        grade === "soon" && "font-medium text-amber-700 dark:text-amber-400",
        grade === "expired" && "font-medium text-destructive",
      )}
    >
      {grade === "expired" ? `Expired ${day}` : `Expires ${day}`}
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
      <DialogContent className="sm:max-w-xl">
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
              {replace
                ? "Change how TLS works for this name. Certificate mode always takes a new certificate and key."
                : "Apps on this server can only use hostnames under a registered domain."}
            </DialogDescription>
          </DialogHeader>
          <div className="grid gap-1.5">
            <Label htmlFor="dom-name">Name</Label>
            <Input
              id="dom-name"
              placeholder="*.example.com"
              autoComplete="off"
              spellCheck={false}
              readOnly={replace !== null}
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
              {nameError || "An exact hostname, or *.example.com for every single-label subdomain."}
            </p>
          </div>
          <div className="grid gap-1.5">
            <Label>TLS</Label>
            <RadioGroup value={tls} onValueChange={(v) => setTLS(v as DomainTLS)} className="gap-3">
              {MODES.map((m) => (
                <label key={m.value} className="flex items-start gap-2.5 text-sm">
                  <RadioGroupItem value={m.value} aria-label={TLS_LABELS[m.value]} className="mt-0.5" />
                  <span>
                    <span className="font-medium">{TLS_LABELS[m.value]}</span>
                    <span className="block text-xs text-muted-foreground">{m.hint}</span>
                  </span>
                </label>
              ))}
            </RadioGroup>
            {tls === "auto" && isWildcard(trimmed) ? (
              <p className="text-xs text-muted-foreground">
                Any single label is accepted; each app hostname under it gets its own certificate.
              </p>
            ) : null}
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
                  value={certificate}
                  onChange={(e) => setCertificate(e.target.value)}
                />
              </div>
              <div className="grid gap-1.5">
                <Label htmlFor="dom-key">Private key (PEM)</Label>
                <Textarea
                  id="dom-key"
                  rows={4}
                  spellCheck={false}
                  className="font-mono text-xs"
                  placeholder="-----BEGIN PRIVATE KEY-----"
                  value={key}
                  onChange={(e) => setKey(e.target.value)}
                />
                <p className="text-xs text-muted-foreground">
                  Encrypted in your browser with this server's key; only the server can read it.
                </p>
                <p className="text-xs text-muted-foreground">
                  The certificate also serves any Auto TLS hostname it matches: no Let's Encrypt
                  certificate is requested for those.
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

  return (
    <Card>
      <CardHeader className="flex-row flex-wrap items-start justify-between gap-x-4 gap-y-3 space-y-0">
        <div className="min-w-0 space-y-1.5">
          <CardTitle>Domains</CardTitle>
          <CardDescription>
            The names apps on this server may use, and how their TLS works.
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
          <p className="rounded-lg border border-dashed py-10 text-center text-sm text-muted-foreground">
            No domains yet. Register one before giving an app a hostname.
          </p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>TLS</TableHead>
                <TableHead className="hidden sm:table-cell">Certificate</TableHead>
                <TableHead className="hidden sm:table-cell">Used by</TableHead>
                <TableHead className="w-20">
                  <span className="sr-only">Actions</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.map((d) => (
                <TableRow key={d.name}>
                  <TableCell className="font-mono">{d.name}</TableCell>
                  <TableCell>
                    <TLSBadge tls={d.tls} />
                  </TableCell>
                  <TableCell className="hidden sm:table-cell">
                    <Expiry domain={d} />
                  </TableCell>
                  <TableCell className="hidden sm:table-cell">
                    <UsedBy ids={d.apps ?? []} names={names} />
                  </TableCell>
                  <TableCell className="py-0.5 text-right">
                    <Button
                      size="icon"
                      variant="ghost"
                      className="size-8 text-muted-foreground"
                      aria-label={`Edit domain ${d.name}`}
                      title={d.tls === "certificate" ? "Change TLS mode or replace certificate" : "Change TLS mode"}
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
              ))}
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
              <code className="break-all font-mono text-foreground">{toDelete?.name}</code> will no
              longer be accepted on this server. It can't be removed while an app uses a hostname no
              other registered domain covers.
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
