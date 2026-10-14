import { useCallback, useEffect, useState } from "react";
import { History, RefreshCw } from "lucide-react";
import { toast } from "sonner";

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import type { Ownership } from "@/components/app-page/ownership";
import { useApps } from "@/context/use-apps";
import type { AppRevisions } from "@/context/apps-context-base";
import { isManagedByTemplate } from "@/lib/templates";
import { cn, relativeTime } from "@/lib/utils";

// A templated app's history explains why it can't be rolled back (the
// template's name may still be loading).
export function HistoryPanel({ appId, ownership }: { appId: string; ownership: Ownership }) {
  const templated = ownership !== null;
  const managedBy = ownership?.instance?.template_name;
  const { getRevisions, rollback, control, refresh } = useApps();
  const [data, setData] = useState<AppRevisions | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      setData(await getRevisions(appId));
    } catch (e) {
      setError(e instanceof Error ? e.message : "Failed to load history");
    } finally {
      setLoading(false);
    }
  }, [appId, getRevisions]);

  useEffect(() => {
    void load();
  }, [load]);

  const doRollback = async (hash: string) => {
    setBusy(true);
    try {
      await rollback(appId, hash);
      toast.success("Rolled back and redeployed");
      await load();
    } catch (e) {
      toast.error("Roll back failed", {
        description: e instanceof Error ? e.message : undefined,
      });
      // A template took the app over meanwhile: show it as managed.
      if (e instanceof Error && isManagedByTemplate(e.message)) void refresh();
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
      await load();
    } catch (e) {
      toast.error("Deploy failed", {
        description: e instanceof Error ? e.message : undefined,
      });
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
        <Button size="sm" variant="outline" onClick={() => void load()}>
          Try again
        </Button>
      </div>
    );
  }
  if (!data || data.revisions.length === 0) {
    return (
      <p className="py-10 text-center text-sm text-muted-foreground">
        No history yet. Every save of the configuration shows up here.
      </p>
    );
  }

  return (
    <div className="max-w-4xl">
      <div className="mb-4 flex items-center justify-between">
        <p className="text-sm text-muted-foreground">
          {data.revisions.length === 1
            ? "1 revision"
            : `${data.revisions.length} revisions`}
          , newest first.{" "}
          {templated
            ? `Managed by ${managedBy ? `template ${managedBy}` : "a template"}: it changes through its parameters or an upgrade; unlink it to roll back.`
            : "Rolling back adds a new revision; nothing is lost."}
        </p>
        <Button
          size="icon"
          variant="ghost"
          className="size-8 text-muted-foreground"
          aria-label="Refresh history"
          title="Refresh history"
          onClick={() => void load()}
          disabled={loading}
        >
          <RefreshCw className={cn("size-4", loading && "animate-spin")} />
        </Button>
      </div>
      <ol>
        {data.revisions.map((rev, i) => {
          const isCurrent = rev.hash === data.current;
          const isDeployed = data.deployed !== "" && rev.hash === data.deployed;
          const isUndeployedDraft = isCurrent && data.deployed !== "" && !isDeployed;
          const isLast = i === data.revisions.length - 1;
          const when = new Date(rev.timestamp * 1000);
          return (
            <li key={rev.hash} className="relative flex gap-4 pb-6 last:pb-0">
              {/* The rail: a dot per revision joined by a line. */}
              {!isLast ? (
                <span
                  className="absolute top-4 bottom-0 left-[7px] w-px bg-border"
                  aria-hidden
                />
              ) : null}
              <span
                className={cn(
                  "relative mt-1 size-[15px] shrink-0 rounded-full border-2 bg-background",
                  isDeployed
                    ? "border-emerald-500 bg-emerald-500 ring-4 ring-emerald-500/15"
                    : isUndeployedDraft
                      ? "border-amber-400"
                      : "border-muted-foreground/40",
                )}
                aria-hidden
              />
              <div className="flex min-w-0 flex-1 flex-wrap items-start gap-x-4 gap-y-2">
                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="truncate text-sm font-medium">
                      {rev.subject}
                    </span>
                    {isDeployed ? (
                      <span className="text-xs font-medium text-emerald-700 dark:text-emerald-400">
                        Live
                      </span>
                    ) : null}
                    {isUndeployedDraft ? (
                      <span className="text-xs font-medium text-amber-700 dark:text-amber-400">
                        Draft, not deployed
                      </span>
                    ) : null}
                  </div>
                  <div className="mt-0.5 flex items-center gap-3 text-xs text-muted-foreground">
                    <code className="font-mono">{rev.hash.slice(0, 8)}</code>
                    <time dateTime={when.toISOString()} title={when.toLocaleString()}>
                      {relativeTime(when)}
                    </time>
                  </div>
                </div>
                {isUndeployedDraft ? (
                  <Button size="sm" onClick={() => void doDeploy()} disabled={busy}>
                    Deploy draft
                  </Button>
                ) : !isCurrent && !templated ? (
                  <AlertDialog>
                    <AlertDialogTrigger asChild>
                      <Button size="sm" variant="ghost" disabled={busy}>
                        <History className="size-4" /> Roll back
                      </Button>
                    </AlertDialogTrigger>
                    <AlertDialogContent>
                      <AlertDialogHeader>
                        <AlertDialogTitle>
                          Roll back to {rev.hash.slice(0, 8)}?
                        </AlertDialogTitle>
                        <AlertDialogDescription>
                          The app's files and variables are restored to this
                          revision as a new history entry, and the app is
                          redeployed. Nothing is lost — you can roll forward
                          again.
                        </AlertDialogDescription>
                      </AlertDialogHeader>
                      <AlertDialogFooter>
                        <AlertDialogCancel>Cancel</AlertDialogCancel>
                        <AlertDialogAction onClick={() => void doRollback(rev.hash)}>
                          Roll back
                        </AlertDialogAction>
                      </AlertDialogFooter>
                    </AlertDialogContent>
                  </AlertDialog>
                ) : null}
              </div>
            </li>
          );
        })}
      </ol>
    </div>
  );
}
