import { useCallback, useEffect, useMemo, useState } from "react";
import { Plus, Trash2 } from "lucide-react";
import { toast } from "sonner";

import { useAppBreadcrumbs } from "@/layouts/use-app-layout";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Spinner } from "@/components/ui/spinner";
import {
  useDocker,
  type Network,
  type Registry,
} from "@/context/use-docker";

export default function DockerPage() {
  const breadcrumbs = useMemo(
    () => [{ label: "Docker" }, { label: "Resources" }],
    [],
  );
  useAppBreadcrumbs(breadcrumbs);

  const docker = useDocker();

  if (!docker.hasServer) {
    return (
      <p className="text-sm text-muted-foreground">
        Select a server to manage its Docker resources.
      </p>
    );
  }

  return (
    <div className="space-y-6">
      <RegistriesCard docker={docker} />
      <NetworksCard docker={docker} />
      <AgentCard docker={docker} />
    </div>
  );
}

function AgentCard({ docker }: { docker: Docker }) {
  const [version, setVersion] = useState("");
  const [busy, setBusy] = useState(false);

  const update = async () => {
    setBusy(true);
    try {
      await docker.updateAgent(version.trim());
      toast.success("Agent update dispatched", {
        description: "The agent will restart on the new version.",
      });
      setVersion("");
    } catch (e) {
      toast.error("Failed to update agent", {
        description: e instanceof Error ? e.message : undefined,
      });
    } finally {
      setBusy(false);
    }
  };

  return (
    <Card>
      <CardHeader>
        <CardTitle>Agent</CardTitle>
      </CardHeader>
      <CardContent>
        <div className="flex items-end gap-2">
          <div className="grid flex-1 gap-1.5">
            <Label htmlFor="agent-version">Update to version</Label>
            <Input
              id="agent-version"
              placeholder="e.g. 1.2.3"
              value={version}
              onChange={(e) => setVersion(e.target.value)}
            />
          </div>
          <Button onClick={() => void update()} disabled={busy || !version.trim()}>
            {busy ? "Updating…" : "Update agent"}
          </Button>
        </div>
        <p className="mt-2 text-xs text-muted-foreground">
          The agent downloads the release, replaces its binary, and restarts.
          Only newer versions are applied.
        </p>
      </CardContent>
    </Card>
  );
}

type Docker = ReturnType<typeof useDocker>;

function RegistriesCard({ docker }: { docker: Docker }) {
  const [items, setItems] = useState<Registry[]>([]);
  const [loading, setLoading] = useState(true);
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [form, setForm] = useState({ address: "", username: "", password: "" });

  const load = useCallback(async () => {
    setLoading(true);
    try {
      setItems(await docker.listRegistries());
    } catch (e) {
      toast.error("Failed to load registries", {
        description: e instanceof Error ? e.message : undefined,
      });
    } finally {
      setLoading(false);
    }
  }, [docker]);

  useEffect(() => {
    void load();
  }, [load]);

  const add = async () => {
    setBusy(true);
    try {
      await docker.createRegistry(form.address, form.username, form.password);
      toast.success("Registry added");
      setOpen(false);
      setForm({ address: "", username: "", password: "" });
      await load();
    } catch (e) {
      toast.error("Failed to add registry", {
        description: e instanceof Error ? e.message : undefined,
      });
    } finally {
      setBusy(false);
    }
  };

  const remove = async (address: string) => {
    try {
      await docker.deleteRegistry(address);
      toast.success("Registry removed");
      await load();
    } catch (e) {
      toast.error("Failed to remove registry", {
        description: e instanceof Error ? e.message : undefined,
      });
    }
  };

  return (
    <Card>
      <CardHeader className="flex-row items-center justify-between space-y-0">
        <CardTitle>Registries</CardTitle>
        <Dialog open={open} onOpenChange={setOpen}>
          <DialogTrigger asChild>
            <Button size="sm">
              <Plus className="size-4" /> Add registry
            </Button>
          </DialogTrigger>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>Add registry</DialogTitle>
            </DialogHeader>
            <div className="grid gap-3">
              <div className="grid gap-1.5">
                <Label htmlFor="reg-addr">Address</Label>
                <Input
                  id="reg-addr"
                  placeholder="registry.example.com"
                  value={form.address}
                  onChange={(e) =>
                    setForm((f) => ({ ...f, address: e.target.value }))
                  }
                />
              </div>
              <div className="grid gap-1.5">
                <Label htmlFor="reg-user">Username</Label>
                <Input
                  id="reg-user"
                  value={form.username}
                  onChange={(e) =>
                    setForm((f) => ({ ...f, username: e.target.value }))
                  }
                />
              </div>
              <div className="grid gap-1.5">
                <Label htmlFor="reg-pass">Password</Label>
                <Input
                  id="reg-pass"
                  type="password"
                  value={form.password}
                  onChange={(e) =>
                    setForm((f) => ({ ...f, password: e.target.value }))
                  }
                />
              </div>
            </div>
            <DialogFooter>
              <Button variant="outline" onClick={() => setOpen(false)}>
                Cancel
              </Button>
              <Button
                onClick={() => void add()}
                disabled={busy || !form.address || !form.username}
              >
                {busy ? "Adding…" : "Add"}
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
      </CardHeader>
      <CardContent>
        {loading ? (
          <Spinner />
        ) : items.length === 0 ? (
          <p className="text-sm text-muted-foreground">No registries.</p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Address</TableHead>
                <TableHead className="w-12" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.map((r) => (
                <TableRow key={r.address}>
                  <TableCell className="font-mono">{r.address}</TableCell>
                  <TableCell>
                    <Button
                      size="icon"
                      variant="ghost"
                      className="size-8"
                      onClick={() => void remove(r.address)}
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
    </Card>
  );
}

function NetworksCard({ docker }: { docker: Docker }) {
  const [items, setItems] = useState<Network[]>([]);
  const [loading, setLoading] = useState(true);
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [name, setName] = useState("");

  const load = useCallback(async () => {
    setLoading(true);
    try {
      setItems(await docker.listNetworks());
    } catch (e) {
      toast.error("Failed to load networks", {
        description: e instanceof Error ? e.message : undefined,
      });
    } finally {
      setLoading(false);
    }
  }, [docker]);

  useEffect(() => {
    void load();
  }, [load]);

  const add = async () => {
    setBusy(true);
    try {
      await docker.createNetwork(name);
      toast.success("Network created");
      setOpen(false);
      setName("");
      await load();
    } catch (e) {
      toast.error("Failed to create network", {
        description: e instanceof Error ? e.message : undefined,
      });
    } finally {
      setBusy(false);
    }
  };

  const remove = async (n: string) => {
    try {
      await docker.deleteNetwork(n);
      toast.success("Network removed");
      await load();
    } catch (e) {
      toast.error("Failed to remove network", {
        description: e instanceof Error ? e.message : undefined,
      });
    }
  };

  // Built-in networks can't be removed; hide the action for them.
  const builtin = new Set(["bridge", "host", "none"]);

  return (
    <Card>
      <CardHeader className="flex-row items-center justify-between space-y-0">
        <CardTitle>Networks</CardTitle>
        <Dialog open={open} onOpenChange={setOpen}>
          <DialogTrigger asChild>
            <Button size="sm">
              <Plus className="size-4" /> Add network
            </Button>
          </DialogTrigger>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>Create network</DialogTitle>
            </DialogHeader>
            <div className="grid gap-1.5">
              <Label htmlFor="net-name">Name</Label>
              <Input
                id="net-name"
                placeholder="my-network"
                value={name}
                onChange={(e) => setName(e.target.value)}
              />
            </div>
            <DialogFooter>
              <Button variant="outline" onClick={() => setOpen(false)}>
                Cancel
              </Button>
              <Button onClick={() => void add()} disabled={busy || !name.trim()}>
                {busy ? "Creating…" : "Create"}
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
      </CardHeader>
      <CardContent>
        {loading ? (
          <Spinner />
        ) : items.length === 0 ? (
          <p className="text-sm text-muted-foreground">No networks.</p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Driver</TableHead>
                <TableHead>Scope</TableHead>
                <TableHead className="w-12" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.map((n) => (
                <TableRow key={n.id ?? n.name}>
                  <TableCell className="font-mono">{n.name}</TableCell>
                  <TableCell>{n.driver ?? "—"}</TableCell>
                  <TableCell>{n.scope ?? "—"}</TableCell>
                  <TableCell>
                    {builtin.has(n.name) ? null : (
                      <Button
                        size="icon"
                        variant="ghost"
                        className="size-8"
                        onClick={() => void remove(n.name)}
                      >
                        <Trash2 className="size-4" />
                      </Button>
                    )}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </CardContent>
    </Card>
  );
}
