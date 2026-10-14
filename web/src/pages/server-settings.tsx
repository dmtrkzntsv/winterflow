import { useState } from "react";
import { Navigate } from "react-router-dom";
import { toast } from "sonner";

import { PageHeader } from "@/components/page-header";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { useDocker, type Docker } from "@/context/use-docker";
import { useServers } from "@/context/use-servers";
import { serverSettingsPath } from "@/lib/routes";
import { DomainsCard } from "@/pages/settings-domains";
import { StoragesCard } from "@/pages/settings-storages";
import { DockerSettings } from "@/pages/settings-docker";

// Settings → Server: one page per section (the sub-menu is SettingsLayout's).
// Each addresses the server in the URL through useDocker / useServers.

function NoServer() {
  return (
    <p className="text-sm text-muted-foreground">
      Select a server to manage its settings.
    </p>
  );
}

// ServerGeneralPage is the server's General page; for now the agent update
// (phase 5 adds the server's facts).
export function ServerGeneralPage() {
  const docker = useDocker();
  const { activeServer } = useServers();
  if (!docker.hasServer || !activeServer) return <NoServer />;
  return (
    <div className="space-y-6">
      <PageHeader title="General" subtitle={activeServer.name} />
      <AgentCard docker={docker} />
    </div>
  );
}

// ServerDomainsPage configures the embedded ingress. A server without it
// (switched to from another server's Domains page, or an old link) opens
// its General page instead.
export function ServerDomainsPage() {
  const docker = useDocker();
  const { activeServer } = useServers();
  if (!docker.hasServer || !activeServer) return <NoServer />;
  if (!activeServer.features?.ingress) {
    return <Navigate replace to={serverSettingsPath(activeServer.id)} />;
  }
  return (
    <div className="space-y-6">
      <PageHeader title="Domains" subtitle={activeServer.name} />
      <DomainsCard docker={docker} />
    </div>
  );
}

export function ServerStoragesPage() {
  const docker = useDocker();
  const { activeServer } = useServers();
  if (!docker.hasServer || !activeServer) return <NoServer />;
  return (
    <div className="space-y-6">
      <PageHeader title="Storages" subtitle={activeServer.name} />
      <StoragesCard docker={docker} />
    </div>
  );
}

// ServerDockerPage is the server's Docker resources (registries, networks,
// volumes); it starts over on another server.
export function ServerDockerPage() {
  const docker = useDocker();
  const { activeServer } = useServers();
  if (!docker.hasServer || !activeServer) return <NoServer />;
  return <DockerSettings key={activeServer.id} docker={docker} />;
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
