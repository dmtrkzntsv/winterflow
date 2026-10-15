import { useState } from "react";
import { AppWindow } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { AppIcon } from "@/components/app-icon";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { EmptyState } from "@/components/empty-state";
import { ErrorText } from "@/components/error-text";
import { Row, RowList } from "@/components/section";
import { StatusPill } from "@/components/status";
import { UpgradeInstanceDialog } from "@/components/upgrade-instance-dialog";
import { useServers } from "@/context/use-servers";
import { useConfirmTarget } from "@/hooks/use-confirm-target";
import { useAppNames } from "@/hooks/use-app-names";
import { useTemplatesApi } from "@/hooks/use-templates-api";
import { appLabel } from "@/lib/kpi";
import { appPath } from "@/lib/routes";
import { appStatus } from "@/lib/status";
import type { InstanceView, TemplateView } from "@/lib/templates";

// instanceName is the name an app was created with (the template's when it
// asked for none).
function instanceName(view: TemplateView, inst: InstanceView): string {
  return appLabel(inst.name || view.name);
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
  const confirmDelete = useConfirmTarget<InstanceView>();
  const [confirmAll, setConfirmAll] = useState(false);
  const serverName = (id: string) => servers.find((s) => s.id === id)?.name ?? "a server";
  // An app renamed since it was created goes by its current name; the
  // created one stands in for an app the catalog doesn't list.
  const current = useAppNames(instances.map((i) => i.server_id));
  const nameOf = (inst: InstanceView) => {
    const live = current.get(inst.app_id);
    return live !== undefined ? appLabel(live) : instanceName(view, inst);
  };

  // remove deletes a pending app; a failure is reported and rethrown so
  // the confirm dialog stays open for a retry.
  const remove = async (inst: InstanceView) => {
    const name = nameOf(inst);
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
          const name = nameOf(inst);
          const old = inst.revision < latest;
          const pending = inst.state === "pending";
          return (
            <Row
              key={inst.key}
              to={appPath(inst.server_id, inst.app_id)}
              leading={<AppIcon name={name} icon={view.body.icon} color={view.body.color} className="size-8 shrink-0 text-sm" />}
              title={name}
              description={`on ${serverName(inst.server_id)}`}
              detail={pending && inst.last_error ? <ErrorText text={inst.last_error} /> : null}
              meta={old ? `Version ${inst.revision} (latest is ${latest})` : `Version ${inst.revision}`}
              actions={
                <>
                  {pending ? (
                    <span title={inst.last_error || undefined}>
                      <StatusPill status={{ tone: "warn", label: "Not created yet" }} />
                    </span>
                  ) : (
                    <StatusPill status={appStatus(inst.status_code)} />
                  )}
                  {old ? (
                    <Button size="sm" variant="outline" className="ml-1" onClick={() => setUpgrading(inst)}>
                      Update
                    </Button>
                  ) : null}
                  {pending ? (
                    <Button
                      size="sm"
                      variant="outline"
                      className="ml-1 text-destructive"
                      disabled={deleting === inst.key}
                      onClick={() => confirmDelete.ask(inst)}
                    >
                      Delete
                    </Button>
                  ) : null}
                </>
              }
            />
          );
        })}
      </RowList>
      <ConfirmDialog
        open={confirmAll}
        onOpenChange={setConfirmAll}
        destructive={false}
        title={`Update ${behind} ${behind === 1 ? "app" : "apps"} to version ${latest}?`}
        description="They are updated one at a time, oldest first, each keeping its own values and hostnames. Stopped apps will be started. An app that needs a new value is skipped (update it on its own); the first failure stops the rest."
        confirmLabel="Update all"
        onConfirm={() => onUpgradeAll?.()}
      />
      <ConfirmDialog
        open={confirmDelete.open}
        onOpenChange={confirmDelete.onOpenChange}
        title={confirmDelete.target ? `Delete ${nameOf(confirmDelete.target)}?` : "Delete"}
        description={
          confirmDelete.target
            ? `Its deploy never succeeded. Deleting removes whatever it left on ${serverName(confirmDelete.target.server_id)}.`
            : undefined
        }
        confirmLabel="Delete"
        pendingLabel="Deleting…"
        onConfirm={() => (confirmDelete.target ? remove(confirmDelete.target) : undefined)}
      />
      {upgrading ? (
        <UpgradeInstanceDialog
          key={upgrading.key}
          instance={upgrading}
          appName={nameOf(upgrading)}
          target={view}
          onClose={() => setUpgrading(null)}
          onDone={onChanged}
        />
      ) : null}
    </div>
  );
}
