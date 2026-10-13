import { useState } from "react";
import { Link } from "react-router-dom";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { AppStatusBadge } from "@/components/app-status-badge";
import { UpgradeInstanceDialog } from "@/components/upgrade-instance-dialog";
import { useServers } from "@/context/use-servers";
import { serverPath } from "@/lib/server-paths";
import { expand, plainValues } from "@/lib/template-params";
import type { InstanceView, TemplateView } from "@/lib/templates";

// TemplateInstances lists the apps a template runs as, across the org's
// servers: where, on which revision, how they are doing, and Upgrade for
// the ones behind the latest revision (view is the latest).
export function TemplateInstances({
  view,
  instances,
  onChanged,
}: {
  view: TemplateView;
  instances: InstanceView[];
  onChanged: () => void;
}) {
  const { servers } = useServers();
  const [upgrading, setUpgrading] = useState<InstanceView | null>(null);
  const serverName = (id: string) => servers.find((s) => s.id === id)?.name ?? id;

  if (instances.length === 0) {
    return (
      <p className="rounded-md border border-dashed p-6 text-center text-sm text-muted-foreground">
        No app uses this template yet. Add one on a server: Apps → From template.
      </p>
    );
  }

  return (
    <>
      <ul className="divide-y rounded-md border">
        {instances.map((inst) => {
          const name = expand(view.body.app_name, plainValues(view.body.parameters, inst.parameters ?? {}));
          const behind = inst.revision < view.latest_revision;
          return (
            <li key={inst.key} className="flex flex-wrap items-center gap-x-4 gap-y-2 px-4 py-3 text-sm">
              <div className="min-w-0 flex-1">
                <Link to={serverPath(inst.server_id, `/app/${inst.app_id}`)} className="font-medium hover:underline">
                  {name || inst.app_id.slice(0, 8)}
                </Link>
                <p className="text-xs text-muted-foreground">on {serverName(inst.server_id)}</p>
              </div>
              <span className={behind ? "text-amber-700 dark:text-amber-400" : "text-muted-foreground"}>
                r{inst.revision}
                {behind ? " · behind" : ""}
              </span>
              {inst.state === "pending" ? (
                <Badge variant="outline" title={inst.last_error || undefined}>
                  Not created yet
                </Badge>
              ) : (
                <AppStatusBadge status={inst.status_code} />
              )}
              {behind ? (
                <Button size="sm" variant="outline" onClick={() => setUpgrading(inst)}>
                  Upgrade
                </Button>
              ) : null}
            </li>
          );
        })}
      </ul>
      {upgrading ? (
        <UpgradeInstanceDialog
          key={upgrading.key}
          instance={upgrading}
          target={view}
          open
          onOpenChange={(o) => {
            if (!o) setUpgrading(null);
          }}
          onDone={onChanged}
        />
      ) : null}
    </>
  );
}
