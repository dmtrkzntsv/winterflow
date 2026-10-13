import { useMemo } from "react";
import { Plus, RefreshCw } from "lucide-react";

import { DeleteVariableDialog } from "@/components/delete-variable-dialog";
import { RolloutStrip } from "@/components/rollout-strip";
import { SharedVariableDialog } from "@/components/shared-variable-dialog";
import { VariablesTable } from "@/components/variables-table";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Spinner } from "@/components/ui/spinner";
import { useRollout } from "@/hooks/use-rollout";
import { useSharedVariablesPage, type Scope } from "@/hooks/use-shared-variables-page";
import { useAppBreadcrumbs } from "@/layouts/use-app-layout";
import { createRolloutsApi, type RolloutsApi } from "@/lib/rollouts";
import { createSharedVariablesApi, type SharedVariablesApi, type SyncState } from "@/lib/shared-variables";

const SYNC_LABEL: Record<SyncState, string> = { synced: "Synced", pending: "Sync pending", failed: "Sync failed" };
const ORG_SCOPE: Scope = { kind: "org" };
const BREADCRUMBS = [{ label: "Organization" }, { label: "Variables" }];

// OrgVariablesPage manages org-level shared variables, available to apps on
// every server of the organization. Secrets show their coverage ("All
// servers" / "Missing on ..."); a new value is encrypted for every keyed
// server, "Provide value" only for the servers missing it. The org's reload
// rollout (reload: true, Reload) shows above with Stop. The logic lives in
// useSharedVariablesPage; this is the layout.
export default function OrgVariablesPage({
  api: injected,
  rollouts: injectedRollouts,
}: {
  api?: SharedVariablesApi;
  rollouts?: RolloutsApi;
}) {
  useAppBreadcrumbs(BREADCRUMBS);
  const api = useMemo(() => injected ?? createSharedVariablesApi(), [injected]);
  const rollouts = useMemo(() => injectedRollouts ?? createRolloutsApi(), [injectedRollouts]);
  const page = useSharedVariablesPage(api, ORG_SCOPE);
  const { org: data } = page;
  const reload = useRollout(rollouts, "vars_reload");

  return (
    <div className="space-y-6">
      <RolloutStrip rollout={reload.rollout} serverName={(id) => page.serverNames.get(id) ?? id} onStop={reload.stop} />
      <Card>
        <CardHeader className="flex-row flex-wrap items-start justify-between gap-x-4 gap-y-3 space-y-0">
          <div className="min-w-0 space-y-1.5">
            <CardTitle>Org variables</CardTitle>
            <CardDescription>
              Available to apps on every server as <code>{"${NAME}"}</code>. A server variable or an app's own
              variable with the same name wins.
            </CardDescription>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <Button size="sm" variant="outline" onClick={() => page.resync()}>
              <RefreshCw className="size-4" /> Resync all servers
            </Button>
            <Button size="sm" onClick={() => page.openDialog({ kind: "create" })}>
              <Plus className="size-4" /> Add variable
            </Button>
          </div>
        </CardHeader>
        <CardContent>
          {data === null ? (
            <div className="flex h-24 items-center justify-center">
              <Spinner />
            </div>
          ) : (
            <VariablesTable
              title="Variables"
              variables={data.variables}
              serverNames={page.serverNames}
              keys={page.keys}
              showCoverage
              onEdit={(v) => page.openDialog({ kind: "edit", variable: v })}
              onDelete={page.setToDelete}
              onProvide={(v) => page.openDialog({ kind: "provide", variable: v })}
              onResync={(v) => page.resync({ variable_id: v.id })}
              onReload={(v) => page.reload({ variable_id: v.id })}
            />
          )}
        </CardContent>
      </Card>

      {data && data.servers.length > 0 ? (
        <section aria-label="Servers" className="space-y-2">
          <h3 className="text-sm font-medium">Servers</h3>
          <ul className="divide-y rounded-md border">
            {data.servers.map((s) => {
              const state = page.sync[s.server_id] ?? s.state;
              return (
                <li key={s.server_id} className="flex items-center justify-between px-3 py-2 text-sm">
                  <span>{s.name}</span>
                  <span className="flex items-center gap-2">
                    {s.has_key ? null : <Badge variant="outline">No key yet</Badge>}
                    <Badge variant={state === "failed" ? "destructive" : "secondary"}>{SYNC_LABEL[state]}</Badge>
                  </span>
                </li>
              );
            })}
          </ul>
        </section>
      ) : null}

      {page.dialog ? (
        <SharedVariableDialog
          key={page.dialogKey}
          open
          mode={page.dialog}
          keys={page.keys}
          onOpenChange={(o) => {
            if (!o) page.closeDialog();
          }}
          onSave={page.save}
        />
      ) : null}

      <DeleteVariableDialog
        variable={page.toDelete}
        description="It is removed from every server. Apps that use it lose the value on their next deploy or reload."
        onCancel={() => page.setToDelete(null)}
        onConfirm={(v) => void page.remove(v)}
      />
    </div>
  );
}
