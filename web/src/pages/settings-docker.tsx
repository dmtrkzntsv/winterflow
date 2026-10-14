import { useCallback, useEffect, useRef, useState } from "react";
import { Plus } from "lucide-react";
import { toast } from "sonner";

import { ConfirmDialog } from "@/components/confirm-dialog";
import { EmptyState } from "@/components/empty-state";
import { ListPlaceholder } from "@/components/list-placeholder";
import { PageHeader } from "@/components/page-header";
import { RefreshButton } from "@/components/refresh-button";
import { RemoveButton } from "@/components/remove-button";
import { ResponsiveDialog } from "@/components/responsive-dialog";
import { Row, RowList, Section } from "@/components/section";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import type { Docker, Network, Registry } from "@/context/use-docker";
import { useConfirmTarget } from "@/hooks/use-confirm-target";
import { returnFocusTo } from "@/lib/focus";
import { VolumesSection } from "@/pages/settings-volumes";

const errorText = (e: unknown) => (e instanceof Error ? e.message : undefined);

// Built-in networks can't be removed: no removal is offered for them.
const BUILTIN_NETWORKS = new Set(["bridge", "host", "none"]);

// useResourceList lists one kind of Docker resource and lists it again on
// demand; a failure keeps the last list and says what went wrong.
function useResourceList<T>(fetchList: () => Promise<T[]>, what: string) {
  const [items, setItems] = useState<T[] | null>(null);
  const [loading, setLoading] = useState(true);
  const reload = useCallback(async () => {
    setLoading(true);
    try {
      setItems(await fetchList());
    } catch (e) {
      toast.error(`Couldn't load ${what}`, { description: errorText(e) });
    } finally {
      setLoading(false);
    }
  }, [fetchList, what]);
  useEffect(() => {
    void reload();
  }, [reload]);
  return { items, loading, reload };
}

function AddRegistryDialog({
  docker,
  open,
  onOpenChange,
  onAdded,
}: {
  docker: Docker;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onAdded: () => Promise<void>;
}) {
  const [form, setForm] = useState({ address: "", username: "", password: "" });
  const [busy, setBusy] = useState(false);
  const canAdd = form.address.trim() !== "" && form.username.trim() !== "" && !busy;

  // The typed password must not outlive the dialog.
  const changeOpen = (next: boolean) => {
    if (!next) setForm({ address: "", username: "", password: "" });
    onOpenChange(next);
  };

  const add = async () => {
    setBusy(true);
    try {
      // useDocker encrypts the password with the server's key.
      await docker.createRegistry(form.address.trim(), form.username.trim(), form.password);
      toast.success("Registry added");
      changeOpen(false);
      await onAdded();
    } catch (e) {
      toast.error("Couldn't add the registry", { description: errorText(e) });
    } finally {
      setBusy(false);
    }
  };

  const field = (key: keyof typeof form, id: string, label: string, props: { type?: string; placeholder?: string } = {}) => (
    <div className="grid gap-1.5">
      <Label htmlFor={id}>{label}</Label>
      <Input
        id={id}
        type={props.type ?? "text"}
        placeholder={props.placeholder}
        autoComplete="off"
        spellCheck={false}
        value={form[key]}
        onChange={(e) => setForm((f) => ({ ...f, [key]: e.target.value }))}
      />
    </div>
  );

  return (
    <ResponsiveDialog
      open={open}
      onOpenChange={changeOpen}
      title="Add registry"
      description="The password is encrypted in your browser; only this server can read it."
      footer={
        <>
          <Button type="button" variant="outline" onClick={() => changeOpen(false)}>
            Cancel
          </Button>
          <Button type="submit" form="registry-form" disabled={!canAdd}>
            {busy ? "Adding…" : "Add"}
          </Button>
        </>
      }
    >
      <form
        id="registry-form"
        className="grid gap-3"
        onSubmit={(e) => {
          e.preventDefault();
          if (canAdd) void add();
        }}
      >
        {field("address", "reg-addr", "Address", { placeholder: "registry.example.com" })}
        {field("username", "reg-user", "Username")}
        {field("password", "reg-pass", "Password", { type: "password" })}
      </form>
    </ResponsiveDialog>
  );
}

function RegistriesSection({ docker }: { docker: Docker }) {
  const { items, loading, reload } = useResourceList<Registry>(docker.listRegistries, "registries");
  const [adding, setAdding] = useState(false);
  const removal = useConfirmTarget<Registry>();
  // A removed row takes its button along: focus then goes to Add registry.
  const opener = useRef<HTMLElement | null>(null);
  const addButton = useRef<HTMLButtonElement | null>(null);

  const remove = async (address: string) => {
    try {
      await docker.deleteRegistry(address);
      toast.success("Registry removed");
      await reload();
    } catch (e) {
      toast.error("Couldn't remove the registry", { description: errorText(e) });
      throw e;
    }
  };

  return (
    <Section
      title="Registries"
      description="Private image registries apps on this server can pull from."
      actions={
        <>
          <RefreshButton label="Refresh registries" loading={loading} onClick={() => void reload()} />
          <Button ref={addButton} size="sm" variant="outline" onClick={() => setAdding(true)}>
            <Plus className="size-4" /> Add registry
          </Button>
        </>
      }
    >
      {items === null ? (
        <ListPlaceholder loading={loading} failed="Couldn't load registries." onRetry={() => void reload()} />
      ) : items.length === 0 ? (
        <EmptyState title="No registries. Add one to pull private images." />
      ) : (
        <RowList label="Registries">
          {items.map((r) => (
            <Row
              key={r.address}
              title={<span className="font-mono">{r.address}</span>}
              actions={
                <RemoveButton
                  label={`Remove registry ${r.address}`}
                  onClick={(trigger) => {
                    opener.current = trigger;
                    removal.ask(r);
                  }}
                />
              }
            />
          ))}
        </RowList>
      )}
      <AddRegistryDialog docker={docker} open={adding} onOpenChange={setAdding} onAdded={reload} />
      <ConfirmDialog
        open={removal.open}
        onOpenChange={removal.onOpenChange}
        title={`Remove ${removal.target?.address ?? "this registry"}?`}
        description="Apps on this server can't pull private images from it until you add it again."
        confirmLabel="Remove registry"
        pendingLabel="Removing…"
        onCloseAutoFocus={(e) => returnFocusTo(opener, addButton)(e)}
        onConfirm={() => (removal.target ? remove(removal.target.address) : undefined)}
      />
    </Section>
  );
}

function CreateNetworkDialog({
  docker,
  open,
  onOpenChange,
  onCreated,
}: {
  docker: Docker;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onCreated: () => Promise<void>;
}) {
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  const canCreate = name.trim() !== "" && !busy;

  const changeOpen = (next: boolean) => {
    if (!next) setName("");
    onOpenChange(next);
  };

  const create = async () => {
    setBusy(true);
    try {
      await docker.createNetwork(name.trim());
      toast.success("Network created");
      changeOpen(false);
      await onCreated();
    } catch (e) {
      toast.error("Couldn't create the network", { description: errorText(e) });
    } finally {
      setBusy(false);
    }
  };

  return (
    <ResponsiveDialog
      open={open}
      onOpenChange={changeOpen}
      title="Add network"
      description="A network apps can join to reach each other by name."
      footer={
        <>
          <Button type="button" variant="outline" onClick={() => changeOpen(false)}>
            Cancel
          </Button>
          <Button type="submit" form="network-form" disabled={!canCreate}>
            {busy ? "Creating…" : "Create"}
          </Button>
        </>
      }
    >
      <form
        id="network-form"
        className="grid gap-1.5"
        onSubmit={(e) => {
          e.preventDefault();
          if (canCreate) void create();
        }}
      >
        <Label htmlFor="net-name">Name</Label>
        <Input
          id="net-name"
          placeholder="my-network"
          autoComplete="off"
          spellCheck={false}
          className="font-mono"
          value={name}
          onChange={(e) => setName(e.target.value)}
        />
      </form>
    </ResponsiveDialog>
  );
}

function NetworksSection({ docker }: { docker: Docker }) {
  const { items, loading, reload } = useResourceList<Network>(docker.listNetworks, "networks");
  const [creating, setCreating] = useState(false);
  const removal = useConfirmTarget<Network>();
  const opener = useRef<HTMLElement | null>(null);
  const addButton = useRef<HTMLButtonElement | null>(null);

  const remove = async (name: string) => {
    try {
      await docker.deleteNetwork(name);
      toast.success("Network removed");
      await reload();
    } catch (e) {
      toast.error("Couldn't remove the network", { description: errorText(e) });
      throw e;
    }
  };

  return (
    <Section
      title="Networks"
      description="Docker networks on this server."
      actions={
        <>
          <RefreshButton label="Refresh networks" loading={loading} onClick={() => void reload()} />
          <Button ref={addButton} size="sm" variant="outline" onClick={() => setCreating(true)}>
            <Plus className="size-4" /> Add network
          </Button>
        </>
      }
    >
      {items === null ? (
        <ListPlaceholder loading={loading} failed="Couldn't load networks." onRetry={() => void reload()} />
      ) : items.length === 0 ? (
        <EmptyState title="No networks yet." />
      ) : (
        <RowList label="Networks">
          {items.map((n) => (
            <Row
              key={n.id ?? n.name}
              title={<span className="font-mono">{n.name}</span>}
              description={[n.driver, n.scope].filter(Boolean).join(" · ") || undefined}
              actions={
                BUILTIN_NETWORKS.has(n.name) ? null : (
                  <RemoveButton
                    label={`Remove network ${n.name}`}
                    onClick={(trigger) => {
                      opener.current = trigger;
                      removal.ask(n);
                    }}
                  />
                )
              }
            />
          ))}
        </RowList>
      )}
      <CreateNetworkDialog docker={docker} open={creating} onOpenChange={setCreating} onCreated={reload} />
      <ConfirmDialog
        open={removal.open}
        onOpenChange={removal.onOpenChange}
        title={`Remove network ${removal.target?.name ?? ""}?`}
        description="Containers still attached to it keep Docker from removing it."
        confirmLabel="Remove network"
        pendingLabel="Removing…"
        onCloseAutoFocus={(e) => returnFocusTo(opener, addButton)(e)}
        onConfirm={() => (removal.target ? remove(removal.target.name) : undefined)}
      />
    </Section>
  );
}

// DockerSettings is Settings → Server → Docker: the server's registries,
// networks and volumes as sections of one page.
export function DockerSettings({ docker }: { docker: Docker }) {
  return (
    <div className="space-y-8">
      <PageHeader title="Docker" subtitle="Registries, networks and volumes on this server." />
      <RegistriesSection docker={docker} />
      <NetworksSection docker={docker} />
      <VolumesSection docker={docker} />
    </div>
  );
}
