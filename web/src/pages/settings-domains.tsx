import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Globe, Plus } from "lucide-react";
import { toast } from "sonner";

import { AppLinks } from "@/components/app-links";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { DomainDialog } from "@/components/domain-dialog";
import { EmptyState } from "@/components/empty-state";
import { ListPlaceholder } from "@/components/list-placeholder";
import { PageHeader } from "@/components/page-header";
import { RefreshButton } from "@/components/refresh-button";
import { RowMenu } from "@/components/row-menu";
import { Row, RowList } from "@/components/section";
import { StatusDot } from "@/components/status";
import { Button } from "@/components/ui/button";
import { useApps } from "@/context/use-apps";
import type { Docker, DomainCreated } from "@/context/use-docker";
import { expiryNote, TLS_MODES, type Domain } from "@/lib/domains";
import { returnFocusTo } from "@/lib/focus";

const byName = (a: Domain, b: Domain) => (a.name < b.name ? -1 : a.name > b.name ? 1 : 0);

// toastResult reports a domain change: success, or a warning when the proxy
// reload that applied it reported problems (the change itself is stored).
function toastResult(done: string, warnings: string[]) {
  if (warnings.length > 0) {
    toast.warning(`${done}, but the proxy reported a problem`, {
      // One line each (toast descriptions keep newlines: index.css).
      description: warnings.join("\n"),
    });
  } else {
    toast.success(done);
  }
}

// DomainsSettings is Settings → Server → Domains: the names apps on this
// server may use, each with its HTTPS state, its certificate's end when it
// is near or past, and the apps using it. Add, edit and remove from here.
export function DomainsSettings({ docker }: { docker: Docker }) {
  const { apps } = useApps();
  const [items, setItems] = useState<Domain[] | null>(null);
  const [loading, setLoading] = useState(true);
  const [dialogOpen, setDialogOpen] = useState(false);
  const [replace, setReplace] = useState<Domain | null>(null);
  const [dialogKey, setDialogKey] = useState(0);
  const [toDelete, setToDelete] = useState<Domain | null>(null);
  // The dialogs have no trigger of their own: focus goes back to what
  // opened them, or to Add domain when that left with its row.
  const opener = useRef<HTMLElement | null>(null);
  const addButton = useRef<HTMLButtonElement | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      setItems(await docker.listDomains());
    } catch (e) {
      toast.error("Couldn't load domains", { description: e instanceof Error ? e.message : undefined });
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
    setItems((prev) => [...(prev ?? []).filter((d) => d.name !== domain.name), domain].sort(byName));
  };

  const remove = async (name: string) => {
    try {
      toastResult("Domain removed", await docker.deleteDomain(name));
      setItems((prev) => prev && prev.filter((d) => d.name !== name));
    } catch (e) {
      // A host only this name covers: the agent's message names the apps.
      toast.error("Couldn't remove domain", { description: e instanceof Error ? e.message : undefined });
      throw e;
    }
  };

  const names = useMemo(() => new Map(apps.map((a) => [a.id, a.name])), [apps]);

  const openDialog = (row: Domain | null, from: HTMLElement | null) => {
    opener.current = from;
    setReplace(row);
    setDialogKey((k) => k + 1);
    setDialogOpen(true);
  };

  const deleteApps = toDelete?.apps ?? [];

  return (
    <div className="space-y-6">
      <PageHeader
        title="Domains"
        subtitle="Addresses apps on this server can use, and how HTTPS works for each."
        actions={
          <>
            <RefreshButton label="Refresh domains" loading={loading} onClick={() => void load()} />
            <Button ref={addButton} className="max-sm:h-10" onClick={(e) => openDialog(null, e.currentTarget)}>
              <Plus className="size-4" /> Add domain
            </Button>
          </>
        }
      />
      {items === null ? (
        <ListPlaceholder loading={loading} failed="Couldn't load domains." onRetry={() => void load()} />
      ) : items.length === 0 ? (
        <EmptyState icon={Globe} title="No domains yet. Add one, like *.example.com, before giving an app an address." />
      ) : (
        <RowList label="Domains">
          {items.map((d) => {
            const note = expiryNote(d);
            return (
              <Row
                key={d.name}
                // Names wrap rather than truncate: the suffix is the part that matters.
                title={<span className="whitespace-normal font-mono [overflow-wrap:anywhere]">{d.name}</span>}
                description={
                  <span className="flex flex-wrap gap-x-2">
                    <span>{TLS_MODES[d.tls].state}</span>
                    {note ? (
                      <span
                        title={d.issuer ? `Issued by ${d.issuer}` : undefined}
                        className="inline-flex items-center gap-1.5 font-medium text-foreground"
                      >
                        <StatusDot tone={note.expired ? "bad" : "warn"} />
                        {note.text}
                      </span>
                    ) : null}
                  </span>
                }
                meta={<AppLinks ids={d.apps ?? []} names={names} />}
                actions={
                  <RowMenu
                    label={`Actions for ${d.name}`}
                    items={[
                      { label: "Edit", onSelect: (trigger) => openDialog(d, trigger) },
                      {
                        label: "Remove",
                        destructive: true,
                        onSelect: (trigger) => {
                          opener.current = trigger;
                          setToDelete(d);
                        },
                      },
                    ]}
                  />
                }
              />
            );
          })}
        </RowList>
      )}
      <DomainDialog
        key={dialogKey}
        docker={docker}
        open={dialogOpen}
        replace={replace}
        returnFocus={opener}
        onOpenChange={setDialogOpen}
        onSaved={saved}
      />
      <ConfirmDialog
        open={toDelete !== null}
        onOpenChange={(o) => !o && setToDelete(null)}
        title="Remove this domain?"
        description={
          <>
            Apps on this server will no longer be able to use{" "}
            <code className="font-mono [overflow-wrap:anywhere] text-foreground">{toDelete?.name}</code>.
            {deleteApps.length > 0 ? (
              <>
                {" "}
                It's used by{" "}
                <span className="font-medium text-foreground">
                  {deleteApps.map((id) => names.get(id) ?? id).join(", ")}
                </span>
                : remove those addresses first, unless another domain on this server covers them.
              </>
            ) : null}
          </>
        }
        confirmLabel="Remove domain"
        pendingLabel="Removing…"
        onCloseAutoFocus={returnFocusTo(opener, addButton)}
        onConfirm={() => (toDelete ? remove(toDelete.name) : undefined)}
      />
    </div>
  );
}
