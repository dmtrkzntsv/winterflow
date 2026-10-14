import { useState, type ReactNode } from "react";
import { Navigate } from "react-router-dom";
import { toast } from "sonner";

import { PageHeader } from "@/components/page-header";
import { Section } from "@/components/section";
import { StatusPill } from "@/components/status";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { isStandalone } from "@/config";
import { useDocker, type Docker } from "@/context/use-docker";
import { useServers } from "@/context/use-servers";
import { serverSettingsPath } from "@/lib/routes";
import { serverFacts, versionLabel } from "@/lib/server-facts";
import { serverStatus } from "@/lib/status";
import { DockerSettings } from "@/pages/settings-docker";
import { DomainsSettings } from "@/pages/settings-domains";
import { StoragesCard } from "@/pages/settings-storages";

// Settings → Server: one page per section (the sub-menu is SettingsLayout's).
// Each addresses the server in the URL through useDocker / useServers; the
// page bodies render their own PageHeader.

function NoServer() {
  return <p className="text-sm text-muted-foreground">Select a server to manage its settings.</p>;
}

// Fact is one line of the server's facts: the label left, the value right;
// stacked on a phone.
function Fact({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-0.5 px-4 py-3 sm:flex-row sm:items-center sm:justify-between sm:gap-4">
      <dt className="text-sm text-muted-foreground">{label}</dt>
      <dd className="text-sm font-medium sm:text-right">{children}</dd>
    </div>
  );
}

// AgentUpdate asks the agent to update itself to a version (the API has no
// latest-version lookup yet: the version is typed).
function AgentUpdate({ docker, running }: { docker: Docker; running: string }) {
  const [version, setVersion] = useState("");
  const [busy, setBusy] = useState(false);
  const target = version.trim();

  const update = async () => {
    setBusy(true);
    try {
      await docker.updateAgent(target);
      toast.success("Update started", { description: "The agent restarts on the new version in a moment." });
      setVersion("");
    } catch (e) {
      toast.error("Couldn't update the agent", { description: e instanceof Error ? e.message : undefined });
    } finally {
      setBusy(false);
    }
  };

  return (
    <Section
      title="Updates"
      description={running ? `Running ${versionLabel(running)}.` : "The agent hasn't reported its version yet."}
    >
      <form
        className="flex flex-col gap-2 sm:flex-row sm:items-end"
        onSubmit={(e) => {
          e.preventDefault();
          if (target && !busy) void update();
        }}
      >
        <div className="grid flex-1 gap-1.5 sm:max-w-xs">
          <Label htmlFor="agent-version">Version</Label>
          <Input
            id="agent-version"
            placeholder="e.g. 1.2.3"
            autoComplete="off"
            spellCheck={false}
            value={version}
            onChange={(e) => setVersion(e.target.value)}
          />
        </div>
        <Button type="submit" className="max-sm:h-10" disabled={busy || !target}>
          {busy ? "Updating…" : "Update"}
        </Button>
      </form>
      <p className="text-xs text-muted-foreground">
        The agent downloads that release, replaces itself and restarts. Only a newer version is applied.
      </p>
    </Section>
  );
}

// ServerGeneralPage is the server's General page: its name, whether its
// agent is connected, what the agent reported about the machine (read-only:
// the API has no rename) and the agent update.
export function ServerGeneralPage() {
  const docker = useDocker();
  const { activeServer, statusByServer } = useServers();
  if (!docker.hasServer || !activeServer) return <NoServer />;
  const caps = activeServer.capabilities ?? {};
  const status = serverStatus({
    liveness: statusByServer[activeServer.id],
    lastSeenAt: activeServer.lastSeenAt,
    standalone: isStandalone,
  });
  return (
    <div className="space-y-8">
      <PageHeader title="General" subtitle="This server and the agent that runs it." />
      <Section title="Server">
        <dl className="divide-y divide-border/70 rounded-xl border bg-card">
          <Fact label="Name">{activeServer.name}</Fact>
          <Fact label="Status">
            <StatusPill status={status} />
          </Fact>
          {serverFacts(caps).map((f) => (
            <Fact key={f.label} label={f.label}>
              {f.value}
            </Fact>
          ))}
        </dl>
      </Section>
      <AgentUpdate key={activeServer.id} docker={docker} running={caps.version ?? ""} />
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
  return <DomainsSettings key={activeServer.id} docker={docker} />;
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
