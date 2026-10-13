import { useMemo } from "react";
import { Plus, RefreshCw, RotateCw } from "lucide-react";

import { DeleteVariableDialog } from "@/components/delete-variable-dialog";
import { ReloadRolloutStrip } from "@/components/rollout-strip";
import { SharedVariableDialog } from "@/components/shared-variable-dialog";
import { VariablesTable } from "@/components/variables-table";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Spinner } from "@/components/ui/spinner";
import { useApps } from "@/context/use-apps";
import { useSharedVariablesPage, type Scope } from "@/hooks/use-shared-variables-page";
import { reloadableStaleCount } from "@/lib/app-status";
import type { RolloutsApi } from "@/lib/rollouts";
import type { SharedVariablesApi, SyncState } from "@/lib/shared-variables";

const SYNC_LABEL: Record<SyncState, string> = { synced: "Synced", pending: "Sync pending", failed: "Sync failed" };

// VariablesCard is the server settings "Variables" tab: this server's
// shared variables (editable), the org's as inherited here (read-only,
// "overridden" when shadowed), the agent's built-ins, a banner for org
// secrets this server has no copy of, and the sync state (from the API,
// then from vars.synced / vars.sync_failed events), and the org's reload
// rollout with Stop. The logic lives in useSharedVariablesPage; this is the
// layout.
export function VariablesCard({
  serverId,
  api,
  rollouts,
}: {
  serverId: string;
  api: SharedVariablesApi;
  rollouts?: RolloutsApi;
}) {
  const { staleVarsByApp, statusByApp } = useApps();
  const scope = useMemo<Scope>(() => ({ kind: "server", serverId }), [serverId]);
  const page = useSharedVariablesPage(api, scope);
  const { server: data } = page;

  // Stopped stale apps apply on their next start; the agent skips them.
  const staleCount = reloadableStaleCount(staleVarsByApp, statusByApp);
  const state = page.sync[serverId] ?? "pending";
  const missing = data?.missing_org_secrets ?? [];
  const firstMissing = data?.inherited.find((v) => missing.includes(v.name));

  return (
    <Card>
      <CardHeader className="flex-row flex-wrap items-start justify-between gap-x-4 gap-y-3 space-y-0">
        <div className="min-w-0 space-y-1.5">
          <CardTitle className="flex items-center gap-2">
            Variables
            {data ? <Badge variant={state === "failed" ? "destructive" : "secondary"}>{SYNC_LABEL[state]}</Badge> : null}
          </CardTitle>
          <CardDescription>
            Available to every app on this server as <code>{"${NAME}"}</code>. Server values override org
            values; an app's own variables override both.
          </CardDescription>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <Button size="sm" variant="outline" onClick={() => page.resync()}>
            <RefreshCw className="size-4" /> Resync
          </Button>
          <Button size="sm" variant="outline" disabled={staleCount === 0} onClick={() => page.reload()}>
            <RotateCw className="size-4" /> Reload stale apps ({staleCount})
          </Button>
          <Button size="sm" onClick={() => page.openDialog({ kind: "create" })}>
            <Plus className="size-4" /> Add variable
          </Button>
        </div>
      </CardHeader>
      <CardContent className="space-y-4">
        <ReloadRolloutStrip api={rollouts} serverNames={page.serverNames} />
        {missing.length > 0 ? (
          <Alert>
            <AlertDescription className="flex flex-wrap items-center justify-between gap-2">
              <span>
                {missing.length === 1
                  ? "1 org secret isn't available on this server"
                  : `${missing.length} org secrets aren't available on this server`}
              </span>
              {firstMissing ? (
                <Button
                  size="sm"
                  variant="outline"
                  onClick={() => page.openDialog({ kind: "provide", variable: firstMissing })}
                >
                  Provide values
                </Button>
              ) : null}
            </AlertDescription>
          </Alert>
        ) : null}
        {data === null ? (
          <div className="flex h-24 items-center justify-center">
            <Spinner />
          </div>
        ) : (
          <VariablesTable
            title="This server"
            variables={data.variables}
            inherited={data.inherited}
            builtins={data.builtins}
            serverNames={page.serverNames}
            onEdit={(v) => page.openDialog({ kind: "edit", variable: v })}
            onDelete={page.setToDelete}
            onProvide={(v) => page.openDialog({ kind: "provide", variable: v })}
            onReload={(v) => page.reload({ names: [v.name] })}
          />
        )}
      </CardContent>

      {page.dialog ? (
        <SharedVariableDialog
          key={page.dialogKey}
          open
          mode={page.dialog}
          keys={page.keys}
          serverId={page.dialog.kind === "provide" ? undefined : serverId}
          onOpenChange={(o) => {
            if (!o) page.closeDialog();
          }}
          onSave={page.save}
        />
      ) : null}

      <DeleteVariableDialog
        variable={page.toDelete}
        description="Apps that use it fall back to the org value, if there is one, on their next deploy or reload."
        onCancel={() => page.setToDelete(null)}
        onConfirm={(v) => void page.remove(v)}
      />
    </Card>
  );
}
