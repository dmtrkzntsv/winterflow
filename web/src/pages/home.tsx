import { Plus, Server } from "lucide-react";

import { Button } from "@/components/ui/button";
import { FiringAlertsLine } from "@/components/alerts/active-alerts";
import { AppGrid } from "@/components/app-grid";
import { EmptyState } from "@/components/empty-state";
import { NewAppButton } from "@/components/new-app/new-app-button";
import { PageHeader } from "@/components/page-header";
import { ServerHealthStrip } from "@/components/server-health-strip";
import { isStandalone } from "@/config";
import { useApps } from "@/context/use-apps";
import { useServers } from "@/context/use-servers";
import { useShellAddServer } from "@/layouts/shell-outlet";
import { appsSubtitle, countApps } from "@/lib/app-summary";

// HomePage is the Apps page (/server/:id): this server's apps, how many
// run, its health in one line, firing alerts in one line, and New app (a
// button that opens the ways to start). The other servers are one switch
// away in the sidebar.
export default function HomePage() {
  const { activeServer, loading } = useServers();
  const { apps, statusByApp, loading: appsLoading } = useApps();
  // The shell's one Add server dialog (none on a standalone install).
  const addServer = useShellAddServer();

  if (!activeServer) {
    return (
      <div className="space-y-6">
        <PageHeader title="Apps" />
        {loading ? null : (
          <EmptyState
            icon={Server}
            title={isStandalone ? "This server isn't ready yet." : "Add a server to run your apps on."}
            action={
              addServer ? (
                <Button onClick={addServer}>
                  <Plus className="size-4" aria-hidden /> Add server
                </Button>
              ) : undefined
            }
          />
        )}
      </div>
    );
  }

  return (
    <div className="space-y-6">
      <PageHeader
        title="Apps"
        // Until the first list arrives "no apps yet" would be a guess, and
        // until the first status report "0 running" would be one too.
        subtitle={
          (appsLoading && apps.length === 0) || (apps.length > 0 && Object.keys(statusByApp).length === 0)
            ? activeServer.name
            : appsSubtitle(activeServer.name, apps.length, countApps(apps, statusByApp))
        }
        actions={
          <NewAppButton serverId={activeServer.id} />
        }
      />
      <div className="space-y-2">
        <ServerHealthStrip server={activeServer} />
        <FiringAlertsLine serverId={activeServer.id} />
      </div>
      <AppGrid />
    </div>
  );
}
