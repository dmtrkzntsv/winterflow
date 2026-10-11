import { useCallback, useEffect, useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { Pencil, Plus, RefreshCw, Trash2 } from "lucide-react";
import { toast } from "sonner";

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
import { DomainDialog } from "@/components/domain-dialog";
import { TLSBadge } from "@/components/tls-badge";
import { type Docker, type DomainCreated } from "@/context/use-docker";
import { useApps } from "@/context/use-apps";
import { expiry, type Domain } from "@/lib/domains";
import { cn } from "@/lib/utils";

// Expiry shows how long a certificate-mode domain's certificate has left:
// a countdown once it's under 30 days (amber), a date otherwise, red once
// past. Other modes renew themselves or have no certificate here.
function Expiry({ domain }: { domain: Domain }) {
  const left = expiry(domain.not_after);
  if (domain.tls !== "certificate" || !left) return null;
  const { grade, days, day } = left;
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

export function DomainsCard({ docker }: { docker: Docker }) {
  const { apps } = useApps();
  const [items, setItems] = useState<Domain[] | null>(null);
  const [loading, setLoading] = useState(true);
  const [dialogOpen, setDialogOpen] = useState(false);
  const [replace, setReplace] = useState<Domain | null>(null);
  const [dialogKey, setDialogKey] = useState(0);
  const [toDelete, setToDelete] = useState<Domain | null>(null);

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

  // A create or delete answers with what changed, so the list is patched in
  // place rather than listed again (Refresh does that).
  const saved = ({ domain, warnings }: DomainCreated) => {
    toastResult(replace ? "Domain updated" : "Domain added", warnings);
    setItems((prev) =>
      [...(prev ?? []).filter((d) => d.name !== domain.name), domain].sort((a, b) =>
        a.name < b.name ? -1 : a.name > b.name ? 1 : 0,
      ),
    );
  };

  const remove = async (name: string) => {
    try {
      toastResult("Domain removed", await docker.deleteDomain(name));
      setItems((prev) => prev && prev.filter((d) => d.name !== name));
    } catch (e) {
      // A host only this name covers: the agent's message names the apps.
      toast.error("Couldn't remove domain", {
        description: e instanceof Error ? e.message : undefined,
      });
    }
  };

  const names = useMemo(() => new Map(apps.map((a) => [a.id, a.name])), [apps]);
  const openDialog = (row: Domain | null) => {
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
            <Plus className="size-4" /> Add domain
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
            No domains yet. Add one, e.g. <code className="font-mono">*.example.com</code>, before
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
        onSaved={saved}
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
                  : remove those hostnames first, unless another domain on this server covers them.
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
