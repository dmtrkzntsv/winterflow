import { useMemo, useRef, type ReactNode } from "react";
import { Braces, Lock, Plus } from "lucide-react";

import { ConfirmDialog } from "@/components/confirm-dialog";
import { Disclosure } from "@/components/disclosure";
import { EmptyState } from "@/components/empty-state";
import { ListPlaceholder } from "@/components/list-placeholder";
import { PageHeader } from "@/components/page-header";
import { ReloadRolloutStrip } from "@/components/rollout-strip";
import { RowMenu } from "@/components/row-menu";
import { Row, RowList } from "@/components/section";
import { SharedVariableDialog } from "@/components/shared-variable-dialog";
import { StatusDot } from "@/components/status";
import { Button } from "@/components/ui/button";
import { isStandalone } from "@/config";
import { useApps } from "@/context/use-apps";
import { useServers } from "@/context/use-servers";
import { useNow } from "@/hooks/use-now";
import { useSharedVariablesSettings } from "@/hooks/use-shared-variables-settings";
import { reloadableStaleCount } from "@/lib/app-status";
import { returnFocusTo } from "@/lib/focus";
import type { RolloutsApi } from "@/lib/rollouts";
import {
  coverageIncomplete,
  coverageLabel,
  createSharedVariablesApi,
  failedServers,
  SYNC_SETTLE_MS,
  variableRows,
  type SharedVariablesApi,
} from "@/lib/shared-variables";
import { cn } from "@/lib/utils";

// Notice is a banner shown only while something needs doing, with the one
// action that does it; label names it for assistive technology.
function Notice({
  label,
  tone,
  action,
  children,
}: {
  label: string;
  tone: "warn" | "bad";
  action: ReactNode;
  children: ReactNode;
}) {
  return (
    <div
      role={tone === "bad" ? "alert" : "status"}
      aria-label={label}
      className={cn(
        "flex flex-wrap items-center gap-3 rounded-xl border px-4 py-2.5 text-sm",
        tone === "bad" ? "border-status-bad/40 bg-status-bad/10" : "border-status-warn/40 bg-status-warn/10",
      )}
    >
      <span className="mr-auto min-w-0">{children}</span>
      {action}
    </div>
  );
}

// SharedVariablesSettingsPage is Settings → Shared variables: one list of
// the organization's variables ("All servers") and the active server's own
// ("{server} only"), the built-ins folded away, and banners only while
// something needs doing (a failed sync, apps on old values, a secret this
// server lacks). New variables are org-wide; "{server} only" is under
// Advanced. With a single server (a standalone install, or an org with one)
// the words and controls about several servers go: no scope, no "Send to
// servers again", a plain Try again after a failed sync. api, rollouts and
// standalone are for tests.
export default function SharedVariablesSettingsPage({
  api: injected,
  rollouts,
  standalone = isStandalone,
}: {
  api?: SharedVariablesApi;
  rollouts?: RolloutsApi;
  standalone?: boolean;
}) {
  const api = useMemo(() => injected ?? createSharedVariablesApi(), [injected]);
  const { activeServer, statusByServer, servers } = useServers();
  const single = standalone || (servers ?? []).length <= 1;
  const { staleVarsByApp, statusByApp } = useApps();
  const page = useSharedVariablesSettings(api, activeServer?.id ?? null);
  const serverName = activeServer?.name ?? "";
  const server = activeServer ? { id: activeServer.id, name: activeServer.name } : null;

  const rows = page.org ? variableRows(page.org.variables, page.server?.variables ?? [], serverName) : null;
  const failed = failedServers(page.sync, page.serverNames);
  // Stopped stale apps apply on their next start; the agent skips them.
  const staleCount = activeServer ? reloadableStaleCount(staleVarsByApp, statusByApp) : 0;
  const missing = page.server?.missing_org_secrets ?? [];
  const firstMissing = page.server?.inherited.find((v) => missing.includes(v.name));
  const builtins = page.server?.builtins ?? [];
  const toDelete = page.toDelete;
  // Keyed servers: where a value can be sent. A sync is "pending" until the
  // agent has taken the latest revision; a failure arrives live.
  const keyedIds = (page.org?.servers ?? []).filter((s) => s.has_key).map((s) => s.server_id);
  // A pending sync is normal for a moment after every save, so it is only
  // mentioned once it has lasted, or when the server's agent is offline.
  // The clock only ticks while a sync is pending (idle cost: none).
  const anyPending = (page.org?.servers ?? []).some((s) => s.has_key && page.sync[s.server_id] === "pending");
  const now = useNow(10_000, anyPending);
  const pending = (page.org?.servers ?? []).filter(
    (s) =>
      s.has_key &&
      page.sync[s.server_id] === "pending" &&
      (statusByServer?.[s.server_id] !== "online" ||
        now - (page.pendingSince[s.server_id] ?? now) > SYNC_SETTLE_MS),
  );
  const unsynced = [...failed.map((f) => f.id), ...pending.map((p) => p.server_id)];
  const syncText = [
    failed.length > 0 ? `Sync to ${failed.map((f) => f.name).join(", ")} failed.` : "",
    pending.length > 0 ? `Sync to ${pending.map((p) => page.serverNames.get(p.server_id) ?? p.name).join(", ")} is pending.` : "",
  ]
    .filter(Boolean)
    .join(" ");
  // Dialogs open from a row's menu or a banner button that may leave with
  // the change: focus goes back to the opener, else to Add variable.
  const opener = useRef<HTMLElement | null>(null);
  const addButton = useRef<HTMLButtonElement | null>(null);
  const from = (trigger: HTMLElement | null) => {
    opener.current = trigger;
  };

  return (
    <div className="space-y-6">
      <PageHeader
        title="Shared variables"
        subtitle="Values every app can use as ${NAME}. An app's own variable with the same name wins."
        actions={
          <>
            <RowMenu
              label="More actions for shared variables"
              items={[
                !single &&
                  keyedIds.length > 0 && {
                    label: "Send to servers again",
                    onSelect: () => page.sendAgain(keyedIds),
                  },
              ]}
            />
            <Button
              ref={addButton}
              disabled={page.org === null}
              onClick={(e) => {
                from(e.currentTarget);
                page.openDialog({ kind: "create" });
              }}
            >
              <Plus className="size-4" /> Add variable
            </Button>
          </>
        }
      />
      <ReloadRolloutStrip api={rollouts} serverNames={page.serverNames} />
      {unsynced.length > 0 ? (
        <Notice
          label="Sync"
          tone={failed.length > 0 ? "bad" : "warn"}
          action={
            <Button size="sm" variant="outline" onClick={() => page.sendAgain(unsynced)}>
              {single ? "Try again" : "Send again"}
            </Button>
          }
        >
          {syncText}
        </Notice>
      ) : null}
      {staleCount > 0 ? (
        <Notice
          label="Old values"
          tone="warn"
          action={
            <Button size="sm" variant="outline" onClick={page.reloadStale}>
              Reload
            </Button>
          }
        >
          {staleCount === 1 ? `1 app on ${serverName} uses old values.` : `${staleCount} apps on ${serverName} use old values.`}
        </Notice>
      ) : null}
      {firstMissing ? (
        <Notice
          label="Missing secrets"
          tone="warn"
          action={
            <Button
              size="sm"
              variant="outline"
              onClick={(e) => {
                from(e.currentTarget);
                page.openDialog({ kind: "provide", variable: firstMissing });
              }}
            >
              Provide value
            </Button>
          }
        >
          {missing.length === 1 ? `1 secret isn't on ${serverName} yet.` : `${missing.length} secrets aren't on ${serverName} yet.`}
        </Notice>
      ) : null}
      {rows === null ? (
        <ListPlaceholder
          loading={!page.loadFailed}
          failed="Couldn't load the shared variables."
          onRetry={() => void page.load()}
        />
      ) : rows.length === 0 ? (
        <EmptyState icon={Braces} title="No shared variables yet. Add one to use it in any app as ${NAME}." />
      ) : (
        <RowList label="Shared variables">
          {rows.map(({ variable: v, scopeLabel, overriddenHere }) => {
            const missingCopies = v.is_secret && v.missing_on.length > 0;
            // Where a secret has no copy: the org's coverage, or this server.
            const gap =
              v.scope === "org"
                ? coverageIncomplete(v, page.keys)
                  ? coverageLabel(v, page.serverNames, page.keys)
                  : ""
                : missingCopies
                  ? `Missing on ${serverName}`
                  : "";
            return (
              <Row
                key={v.id}
                title={<span className="font-mono">{v.name}</span>}
                description={
                  <span className="flex flex-wrap items-center gap-x-2">
                    {/* Never a secret's value: the API only ever sends "<encrypted>". */}
                    {v.is_secret ? (
                      <span className="inline-flex items-center gap-1">
                        <Lock className="size-3" aria-hidden />
                        Secret
                      </span>
                    ) : (
                      <code className="truncate">{v.value}</code>
                    )}
                    {v.description ? <span>· {v.description}</span> : null}
                    {overriddenHere ? <span>· {serverName} uses its own value</span> : null}
                    {gap ? (
                      <span className="inline-flex items-center gap-1.5 font-medium text-foreground">
                        <StatusDot tone="warn" />
                        {gap}
                      </span>
                    ) : null}
                  </span>
                }
                meta={single ? undefined : scopeLabel}
                actions={
                  <>
                    {missingCopies ? (
                      <Button
                        size="sm"
                        variant="outline"
                        onClick={(e) => {
                          from(e.currentTarget);
                          page.openDialog({ kind: "provide", variable: v });
                        }}
                      >
                        Provide value
                      </Button>
                    ) : null}
                    <RowMenu
                      label={`Actions for ${v.name}`}
                      items={[
                        {
                          label: "Edit",
                          onSelect: (trigger) => {
                            from(trigger);
                            page.openDialog({ kind: "edit", variable: v });
                          },
                        },
                        { label: "Reload apps using it", onSelect: () => page.reloadVariable(v) },
                        !single &&
                          v.scope === "org" && { label: "Send to servers again", onSelect: () => page.resendVariable(v) },
                        {
                          label: "Delete",
                          destructive: true,
                          onSelect: (trigger) => {
                            from(trigger);
                            page.setToDelete(v);
                          },
                        },
                      ]}
                    />
                  </>
                }
              />
            );
          })}
        </RowList>
      )}
      {builtins.length > 0 ? (
        <Disclosure label={`Built-in variables (${builtins.length})`}>
          <RowList label="Built-in variables">
            {builtins.map((b) => (
              <Row
                key={b.name}
                title={<span className="font-mono">{b.name}</span>}
                description={b.value ? <code>{b.value}</code> : "Set by the agent"}
                meta={b.description}
              />
            ))}
          </RowList>
        </Disclosure>
      ) : null}
      {page.dialog ? (
        <SharedVariableDialog
          key={page.dialogKey}
          open
          mode={page.dialog}
          keys={page.keys}
          server={server}
          chooseScope={!single && server !== null}
          singleServer={single}
          onOpenChange={(o) => {
            if (!o) page.closeDialog();
          }}
          onSave={page.save}
          onCloseAutoFocus={(e) => returnFocusTo(opener, addButton)(e)}
        />
      ) : null}
      <ConfirmDialog
        open={page.deleteOpen}
        onOpenChange={(o) => !o && page.setToDelete(null)}
        title={`Delete ${toDelete?.name ?? "this variable"}?`}
        description={
          toDelete?.scope === "server"
            ? "Apps that use it fall back to the organization's value, if there is one, on their next deploy or reload."
            : "It's removed from every server. Apps that use it lose the value on their next deploy or reload."
        }
        confirmLabel="Delete variable"
        pendingLabel="Deleting…"
        onCloseAutoFocus={(e) => returnFocusTo(opener, addButton)(e)}
        onConfirm={() => (toDelete ? page.remove(toDelete) : undefined)}
      />
    </div>
  );
}
