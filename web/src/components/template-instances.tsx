import { useState } from "react";
import { Link } from "react-router-dom";
import { AppWindow } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { AppIcon } from "@/components/app-icon";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { EmptyState } from "@/components/empty-state";
import { ErrorText } from "@/components/error-text";
import { RowList } from "@/components/section";
import { StatusPill } from "@/components/status";
import { UpgradeInstanceDialog } from "@/components/upgrade-instance-dialog";
import { useServers } from "@/context/use-servers";
import { useTemplatesApi } from "@/hooks/use-templates-api";
import { appLabel } from "@/lib/kpi";
import { appPath } from "@/lib/routes";
import { appStatus } from "@/lib/status";
import { expand, plainValues } from "@/lib/template-params";
import type { InstanceView, TemplateView } from "@/lib/templates";

// instanceName is an app's name as the latest version renders it.
function instanceName(view: TemplateView, inst: InstanceView): string {
  return appLabel(expand(view.body.app_name, plainValues(view.body.parameters, inst.parameters ?? {})));
}

// TemplateInstances is the "Apps using it" tab: the apps a template runs
// as, across the organization's servers — where, on which version, how they
// are doing, and Update for the ones on an older version (view is the
// latest). A pending app (its deploy never succeeded) shows why and offers
// Delete: no other page reaches it, and it keeps the template from being
// deleted. With onUpgradeAll, "Update all apps" (confirmed first) queues
// the update of every created app on an older version; it waits while an
// update of the template runs.
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
  const [confirmDelete, setConfirmDelete] = useState<InstanceView | null>(null);
  const [confirmAll, setConfirmAll] = useState(false);
  const serverName = (id: string) => servers.find((s) => s.id === id)?.name ?? id;

  // remove deletes a pending app; a failure is reported and rethrown so
  // the confirm dialog stays open for a retry.
  const remove = async (inst: InstanceView) => {
    const name = instanceName(view, inst);
    setDeleting(inst.key);
    try {
      await api.deleteInstance(inst.server_id, inst.key);
    } catch (e) {
      toast.error(`Couldn't delete ${name}`, { description: e instanceof Error ? e.message : undefined });
      throw e;
    } finally {
      setDeleting(null);
    }
    toast.success(`Deleted ${name}`);
    onChanged();
  };

  if (instances.length === 0) {
    return (
      <EmptyState
        icon={AppWindow}
        title="No app uses this template yet. Add one from a server: New app → From a template."
      />
    );
  }

  const latest = view.latest_revision;
  const behind = instances.filter((i) => i.state === "created" && i.revision < latest).length;

  return (
    <div className="space-y-3">
      {onUpgradeAll && behind > 0 ? (
        <div className="flex flex-wrap items-center justify-between gap-3 rounded-xl border bg-muted/30 px-4 py-3 text-sm">
          <span>
            {behind} {behind === 1 ? "app uses" : "apps use"} an older version. The latest is version {latest}.
          </span>
          <Button
            size="sm"
            disabled={rolloutActive}
            title={rolloutActive ? "An update is running" : undefined}
            onClick={() => setConfirmAll(true)}
          >
            Update all apps
          </Button>
        </div>
      ) : null}
      <RowList label="Apps using this template">
        {instances.map((inst) => {
          const name = instanceName(view, inst);
          const old = inst.revision < latest;
          const pending = inst.state === "pending";
          return (
            <li
              key={inst.key}
              className="flex flex-col gap-2 px-4 py-3 text-sm transition-colors duration-150 hover:bg-muted/40 motion-reduce:transition-none sm:flex-row sm:items-center sm:gap-4 max-sm:rounded-xl max-sm:border max-sm:bg-card"
            >
              <div className="flex min-w-0 flex-1 items-center gap-3">
                <AppIcon name={name} icon={view.body.icon} color={view.body.color} className="size-8 shrink-0 text-sm" />
                <div className="min-w-0">
                  <Link to={appPath(inst.server_id, inst.app_id)} className="font-medium hover:underline">
                    {name}
                  </Link>
                  <p className="text-xs text-muted-foreground">on {serverName(inst.server_id)}</p>
                  {pending && inst.last_error ? <ErrorText text={inst.last_error} className="text-xs" /> : null}
                </div>
              </div>
              <span className="text-xs text-muted-foreground">
                {old ? `Version ${inst.revision} (latest is ${latest})` : `Version ${inst.revision}`}
              </span>
              {pending ? (
                <span title={inst.last_error || undefined}>
                  <StatusPill status={{ tone: "warn", label: "Not created yet" }} />
                </span>
              ) : (
                <StatusPill status={appStatus(inst.status_code)} />
              )}
              {old || pending ? (
                <div className="flex items-center gap-2">
                  {old ? (
                    <Button size="sm" variant="outline" onClick={() => setUpgrading(inst)}>
                      Update
                    </Button>
                  ) : null}
                  {pending ? (
                    <Button
                      size="sm"
                      variant="outline"
                      className="text-destructive"
                      disabled={deleting === inst.key}
                      onClick={() => setConfirmDelete(inst)}
                    >
                      Delete
                    </Button>
                  ) : null}
                </div>
              ) : null}
            </li>
          );
        })}
      </RowList>
      <ConfirmDialog
        open={confirmAll}
        onOpenChange={setConfirmAll}
        destructive={false}
        title={`Update ${behind} ${behind === 1 ? "app" : "apps"} to version ${latest}?`}
        description="They are updated one at a time, oldest first, each keeping its own values. Stopped apps will be started. An app that needs a new value is skipped (update it on its own); the first failure stops the rest."
        confirmLabel="Update all"
        onConfirm={() => onUpgradeAll?.()}
      />
      <ConfirmDialog
        open={confirmDelete !== null}
        onOpenChange={(open) => {
          if (!open) setConfirmDelete(null);
        }}
        title={confirmDelete ? `Delete ${instanceName(view, confirmDelete)}?` : "Delete"}
        description={
          confirmDelete
            ? `Its deploy never succeeded. Deleting removes whatever it left on ${serverName(confirmDelete.server_id)}.`
            : undefined
        }
        confirmLabel="Delete"
        pendingLabel="Deleting…"
        onConfirm={() => (confirmDelete ? remove(confirmDelete) : undefined)}
      />
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
    </div>
  );
}
