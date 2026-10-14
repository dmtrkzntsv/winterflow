import { useState } from "react";
import { Link } from "react-router-dom";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { AppStatusIndicator } from "@/components/app-status-badge";
import { ConfirmButton } from "@/components/confirm-button";
import { ErrorText } from "@/components/error-text";
import { EmptyNote } from "@/components/page-states";
import { UpgradeInstanceDialog } from "@/components/upgrade-instance-dialog";
import { useServers } from "@/context/use-servers";
import { useTemplatesApi } from "@/hooks/use-templates-api";
import { appLabel } from "@/lib/kpi";
import { serverPath } from "@/lib/server-paths";
import { expand, plainValues } from "@/lib/template-params";
import type { InstanceView, TemplateView } from "@/lib/templates";

// instanceName is an instance's app name as the latest revision renders it.
function instanceName(view: TemplateView, inst: InstanceView): string {
  return appLabel(expand(view.body.app_name, plainValues(view.body.parameters, inst.parameters ?? {})));
}

// TemplateInstances lists the apps a template runs as, across the org's
// servers: where, on which revision, how they are doing, and Upgrade for
// the ones behind the latest revision (view is the latest). A pending
// instance (its deploy never succeeded) shows why and offers Delete: no
// other page reaches it, and it keeps the template from being deleted.
// With onUpgradeAll, "Upgrade all to rN" (confirmed first) queues the
// upgrade of every created instance behind; it waits while a rollout of
// the template is active.
export function TemplateInstances({
  view,
  instances,
  onChanged,
  onUpgradeAll,
  rolloutActive = false,
}: {
  view: TemplateView;
  instances: InstanceView[];
  onChanged: () => void;
  onUpgradeAll?: () => void;
  rolloutActive?: boolean;
}) {
  const { servers } = useServers();
  const api = useTemplatesApi();
  const [upgrading, setUpgrading] = useState<InstanceView | null>(null);
  const [deleting, setDeleting] = useState<string | null>(null); // key
  const serverName = (id: string) => servers.find((s) => s.id === id)?.name ?? id;

  const remove = async (inst: InstanceView, name: string) => {
    setDeleting(inst.key);
    try {
      await api.deleteInstance(inst.server_id, inst.key);
      toast.success(`Deleted ${name}`);
      onChanged();
    } catch (e) {
      toast.error(`Couldn't delete ${name}`, { description: e instanceof Error ? e.message : undefined });
    } finally {
      setDeleting(null);
    }
  };

  if (instances.length === 0) {
    return (
      <EmptyNote className="p-6 text-center">
        No app uses this template yet. Add one on a server: Apps → From template.
      </EmptyNote>
    );
  }

  const behind = instances.filter((i) => i.state === "created" && i.revision < view.latest_revision).length;

  return (
    <>
      {onUpgradeAll && behind > 0 ? (
        <div className="mb-3 flex items-center justify-end gap-3">
          <span className="text-sm text-muted-foreground">
            {behind} {behind === 1 ? "app is" : "apps are"} behind r{view.latest_revision}
          </span>
          <ConfirmButton
            trigger={
              <Button size="sm" disabled={rolloutActive} title={rolloutActive ? "An upgrade is running" : undefined}>
                Upgrade all to r{view.latest_revision}
              </Button>
            }
            title={`Upgrade ${behind} ${behind === 1 ? "app" : "apps"} to r${view.latest_revision}?`}
            description="They are upgraded one at a time, oldest first, each with its own values. Stopped apps will be started. An app that needs a new value is skipped (upgrade it on its own); the first failure stops the rest."
            confirmLabel="Upgrade all"
            onConfirm={onUpgradeAll}
          />
        </div>
      ) : null}
      <ul className="divide-y rounded-md border">
        {instances.map((inst) => {
          const name = instanceName(view, inst);
          const behind = inst.revision < view.latest_revision;
          return (
            <li key={inst.key} className="flex flex-wrap items-center gap-x-4 gap-y-2 px-4 py-3 text-sm">
              <div className="min-w-0 flex-1">
                <Link to={serverPath(inst.server_id, `/app/${inst.app_id}`)} className="font-medium hover:underline">
                  {name}
                </Link>
                <p className="text-xs text-muted-foreground">on {serverName(inst.server_id)}</p>
                {inst.state === "pending" && inst.last_error ? (
                  <ErrorText text={inst.last_error} className="text-xs" />
                ) : null}
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
                <AppStatusIndicator status={inst.status_code} />
              )}
              {behind ? (
                <Button size="sm" variant="outline" onClick={() => setUpgrading(inst)}>
                  Upgrade
                </Button>
              ) : null}
              {inst.state === "pending" ? (
                <ConfirmButton
                  trigger={
                    <Button size="sm" variant="outline" className="text-destructive" disabled={deleting === inst.key}>
                      Delete
                    </Button>
                  }
                  title={`Delete ${name}?`}
                  description={`Its deploy never succeeded. Deleting removes whatever it left on ${serverName(inst.server_id)}.`}
                  confirmLabel="Delete"
                  onConfirm={() => void remove(inst, name)}
                />
              ) : null}
            </li>
          );
        })}
      </ul>
      {upgrading ? (
        <UpgradeInstanceDialog
          key={upgrading.key}
          instance={upgrading}
          appName={instanceName(view, upgrading)}
          target={view}
          onClose={() => setUpgrading(null)}
          onDone={onChanged}
        />
      ) : null}
    </>
  );
}
