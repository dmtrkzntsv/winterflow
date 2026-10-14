import { useState } from "react";
import { History, RefreshCw } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import type { Ownership } from "@/components/app-page/ownership";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { RevisionBadge } from "@/components/app-page/revision-badge";
import type { AppRevision } from "@/context/apps-context-base";
import { useApps } from "@/context/use-apps";
import { useAppRevisions } from "@/hooks/use-app-revisions";
import { useConfirmTarget } from "@/hooks/use-confirm-target";
import { revisionLabel, whenLabel } from "@/lib/revision-labels";
import { revisionFlags } from "@/lib/revisions";
import { isManagedByTemplate } from "@/lib/templates";
import { cn, relativeTime } from "@/lib/utils";

// HistoryPanel lists an app's versions, newest first: what changed (in
// plain words, see revisionLabel) and when (the commit hash on hover), which one is live, an undeployed draft
// with Deploy draft, and Roll back for an older one — not for an app a
// template manages: its template owns the files (its name may still be
// loading). onChanged is called after a roll back went through, so what
// shows the app's files (the Configuration editor) can read them again; a
// draft deploy leaves the files as they are, so it doesn't call it.
export function HistoryPanel({
  appId,
  ownership,
  onChanged,
}: {
  appId: string;
  ownership: Ownership;
  onChanged?: () => void;
}) {
  const templated = ownership !== null;
  const managedBy = ownership?.instance?.template_name;
  const { rollback, control, refresh } = useApps();
  const { data, loading, error, reload } = useAppRevisions(appId);
  const [busy, setBusy] = useState(false);
  const confirm = useConfirmTarget<AppRevision>();
  const target = confirm.target;

  // doRollback reports its failure and throws it again, so the confirm
  // dialog stays open for a retry.
  const doRollback = async (hash: string) => {
    setBusy(true);
    try {
      await rollback(appId, hash);
      toast.success("Rolled back and redeployed");
      onChanged?.();
    } catch (e) {
      toast.error("Roll back failed", { description: e instanceof Error ? e.message : undefined });
      // A template took the app over meanwhile: show it as managed.
      if (e instanceof Error && isManagedByTemplate(e.message)) void refresh();
      throw e;
    } finally {
      setBusy(false);
    }
  };

  // Deploying the current draft is a plain start: the worktree already holds
  // HEAD, so materialize + compose up brings the draft live.
  const doDeploy = async () => {
    setBusy(true);
    try {
      await control(appId, "start");
      toast.success("Draft deployed");
    } catch (e) {
      toast.error("Deploy failed", { description: e instanceof Error ? e.message : undefined });
    } finally {
      setBusy(false);
    }
  };

  if (loading && !data) {
    return (
      <div className="flex h-40 items-center justify-center">
        <Spinner />
      </div>
    );
  }
  if (error) {
    return (
      <div className="flex h-40 flex-col items-center justify-center gap-3">
        <p className="text-sm text-destructive">{error}</p>
        <Button size="sm" variant="outline" onClick={() => void reload()}>
          Try again
        </Button>
      </div>
    );
  }
  if (!data || data.revisions.length === 0) {
    return <p className="py-10 text-center text-sm text-muted-foreground">No history yet. Every saved change shows up here.</p>;
  }

  return (
    <div className="space-y-4">
      <div className="flex items-start justify-between gap-3">
        <p className="text-sm text-muted-foreground">
          {data.revisions.length === 1 ? "1 version" : `${data.revisions.length} versions`}.{" "}
          {templated
            ? `Managed by ${managedBy ? `template ${managedBy}` : "a template"}: it changes through its values or an update; detach it to roll back.`
            : "Rolling back adds a new version; nothing is lost."}
        </p>
        <Button
          size="icon"
          variant="ghost"
          className="size-8 shrink-0 text-muted-foreground"
          aria-label="Refresh history"
          title="Refresh history"
          onClick={() => void reload()}
          disabled={loading}
        >
          <RefreshCw className={cn("size-4", loading && "animate-spin motion-reduce:animate-none")} />
        </Button>
      </div>
      <ol aria-label="Versions">
        {data.revisions.map((rev, i) => {
          const { isCurrent, isDeployed, isUndeployedDraft } = revisionFlags(data, rev.hash);
          const isLast = i === data.revisions.length - 1;
          const when = new Date(rev.timestamp * 1000);
          return (
            <li key={rev.hash} className="relative flex gap-3 pb-5 last:pb-0">
              {!isLast ? <span className="absolute top-4 bottom-0 left-[6px] w-px bg-border" aria-hidden /> : null}
              <span
                className={cn(
                  "relative mt-1 size-[13px] shrink-0 rounded-full border-2 bg-background",
                  isDeployed
                    ? "border-status-ok bg-status-ok ring-4 ring-status-ok/15"
                    : isUndeployedDraft
                      ? "border-status-warn"
                      : "border-muted-foreground/40",
                )}
                aria-hidden
              />
              <div className="flex min-w-0 flex-1 flex-wrap items-start gap-x-3 gap-y-2">
                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="truncate text-sm font-medium" title={`Commit ${rev.hash.slice(0, 8)}`}>
                      {revisionLabel(data.revisions, i)}
                    </span>
                    <RevisionBadge isDeployed={isDeployed} isUndeployedDraft={isUndeployedDraft} draftLabel="Draft, not deployed" />
                  </div>
                  <time className="text-xs text-muted-foreground" dateTime={when.toISOString()} title={when.toLocaleString()}>
                    {relativeTime(when)}
                  </time>
                </div>
                {isUndeployedDraft ? (
                  <Button size="sm" onClick={() => void doDeploy()} disabled={busy || loading}>
                    Deploy draft
                  </Button>
                ) : !isCurrent && !templated ? (
                  <Button size="sm" variant="ghost" disabled={busy || loading} onClick={() => confirm.ask(rev)}>
                    <History className="size-4" aria-hidden /> Roll back
                  </Button>
                ) : null}
              </div>
            </li>
          );
        })}
      </ol>
      <ConfirmDialog
        open={confirm.open}
        onOpenChange={confirm.onOpenChange}
        title={target ? `Roll back to the version from ${whenLabel(new Date(target.timestamp * 1000))}?` : "Roll back?"}
        description="The app's files and variables go back to this version as a new entry in the history, and the app is redeployed. Nothing is lost: you can roll forward again."
        confirmLabel="Roll back"
        pendingLabel="Rolling back…"
        destructive={false}
        onConfirm={async () => {
          if (target) await doRollback(target.hash);
        }}
      />
    </div>
  );
}
