import { useEffect, useRef, useState, type ReactNode } from "react";
import { Link } from "react-router-dom";
import { ArrowRight, ExternalLink } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { AppMetricBlocks } from "@/components/app-metrics";
import { RevisionBadge } from "@/components/app-page/revision-badge";
import { InlineError } from "@/components/inline-error";
import { RestorePointsDialog } from "@/components/restore-points-dialog";
import { Section } from "@/components/section";
import { StatusDot, StatusPill } from "@/components/status";
import { AppStatusCode, type App } from "@/context/apps-context-base";
import { useApps } from "@/context/use-apps";
import { useBackups } from "@/context/use-backups";
import { useServerPath } from "@/context/use-server-path";
import { useServers } from "@/context/use-servers";
import { useAppRevisions } from "@/hooks/use-app-revisions";
import { useRunBackup } from "@/hooks/use-run-backup";
import { appAddresses, containerHint, containersSummary, serviceLabels } from "@/lib/app-summary";
import { backupRowState, restoringBackup, shownHealth, useActivity, useOverlays } from "@/lib/backup-activity";
import type { EffectiveBackup } from "@/lib/backups";
import type { RegisteredDomain } from "@/lib/domains";
import { revisionFlags } from "@/lib/revisions";
import { appSub } from "@/lib/routes";
import { appStatus, backupStatus, RESTORING } from "@/lib/status";
import { relativeTime } from "@/lib/utils";

const CARD = "min-w-0 rounded-xl border bg-card p-4";
const LINK =
  "inline-flex min-h-8 items-center gap-1 text-sm font-medium text-muted-foreground transition-colors duration-150 hover:text-foreground max-sm:min-h-10";

// Retry is a failed read's message with the way to try again.
function Retry({ message, onRetry }: { message: string; onRetry: () => void }) {
  return (
    <InlineError>
      {message}{" "}
      <button
        type="button"
        className="inline-flex min-h-8 items-center underline underline-offset-4 max-sm:min-h-10"
        onClick={onRetry}
      >
        Retry
      </button>
    </InlineError>
  );
}

// StatusWidget is the app's containers by compose service, one line on how
// many run, and what is wrong with the ones that don't. The state and
// Restart/Start are in the header; the API reports no start time, so there
// is no "since".
function StatusWidget({ app, status }: { app: App; status: number | undefined }) {
  const { containersByApp } = useApps();
  const containers = containersByApp?.[app.id] ?? [];
  const labels = serviceLabels(app.id, containers);
  const stopped = status === AppStatusCode.Stopped;
  return (
    <Section title="Status" className={CARD}>
      <p className="text-sm font-medium">{containersSummary(status, containers)}</p>
      {containers.length > 0 && !stopped ? (
        <ul aria-label="Containers" className="space-y-1.5">
          {containers.map((c, i) => {
            const info = appStatus(c.status_code);
            const hint = containerHint(c);
            return (
              <li key={c.container_id || c.name} className="flex min-w-0 items-start gap-2">
                <StatusDot tone={info.tone} pulse={info.pulse} label={info.label} className="mt-1.5" />
                <span className="min-w-0">
                  <span className="block truncate font-mono text-xs" title={c.name}>
                    {labels[i]}
                  </span>
                  {hint ? <span className="block text-xs text-muted-foreground">{hint}</span> : null}
                </span>
              </li>
            );
          })}
        </ul>
      ) : null}
    </Section>
  );
}

// AccessWidget is where the app is reachable: each address with how it is
// served (from its registered domain's TLS mode; nothing when no domain
// covers it); without one, the way to add a domain (only when this server
// runs the built-in proxy).
function AccessWidget({ app, registrations }: { app: App; registrations: RegisteredDomain[] }) {
  const { activeServer } = useServers();
  const toServer = useServerPath();
  const addresses = appAddresses(app, registrations);
  return (
    <Section title="Access" className={CARD}>
      {addresses.length === 0 ? (
        <div className="space-y-2">
          <p className="text-sm text-muted-foreground">No public address.</p>
          {activeServer?.features?.ingress ? (
            <Button asChild size="sm" variant="outline">
              <Link to={toServer(appSub(app.id, "configuration"))}>Add a domain</Link>
            </Button>
          ) : null}
        </div>
      ) : (
        <ul className="space-y-1.5">
          {addresses.map((a) => (
            <li key={a.host} className="flex min-w-0 items-center justify-between gap-3 text-sm">
              <a
                href={a.url}
                target="_blank"
                rel="noreferrer"
                className="inline-flex min-w-0 items-center gap-1 font-medium hover:underline max-sm:min-h-10"
              >
                <span className="truncate">{a.host}</span>
                <ExternalLink className="size-3.5 shrink-0" aria-hidden />
              </a>
              {a.tls ? (
                <span className="inline-flex shrink-0 items-center gap-1.5 text-xs text-muted-foreground">
                  <StatusDot tone={a.tls === "https" ? "ok" : "idle"} />
                  {a.tls === "https" ? "HTTPS" : "Behind your proxy"}
                </span>
              ) : null}
            </li>
          ))}
        </ul>
      )}
    </Section>
  );
}

// ResourcesWidget is the app's CPU, memory, disk and network now, with the
// last hour as sparklines.
function ResourcesWidget({ app }: { app: App }) {
  const toServer = useServerPath();
  return (
    <Section
      title="Resources"
      className={CARD}
      actions={
        <Link to={toServer(appSub(app.id, "metrics"))} className={LINK}>
          View metrics <ArrowRight className="size-4" aria-hidden />
        </Link>
      }
    >
      <AppMetricBlocks serverId={app.serverId} appId={app.id} />
    </Section>
  );
}

// What one read of the app's backups said, for which app and attempt.
type BackupsRead = { appId: string; attempt: number; list: EffectiveBackup[] | null; error: string };

// BackupsWidget is each set-up backup's health (last copy, next one) with
// Back up now and Restore for the built-in ones; without any, the way to
// set one up. The list (and when each runs) is read from the app once;
// onSettled says when that read ended, so the history can follow it.
function BackupsWidget({ app, onSettled }: { app: App; onSettled: (appId: string) => void }) {
  const { getApp, backupsByApp } = useApps();
  const backups = useBackups();
  const runOne = useRunBackup(app.id);
  const activity = useActivity(app.id);
  const overlays = useOverlays(app.id);
  const toServer = useServerPath();
  const [attempt, setAttempt] = useState(0);
  const [read, setRead] = useState<BackupsRead | null>(null);
  const [points, setPoints] = useState<EffectiveBackup | null>(null);
  const opener = useRef<HTMLElement | null>(null);

  // Retry and a finished restore bump the attempt, which reads again.
  useEffect(() => {
    let live = true;
    const settle = (list: EffectiveBackup[] | null, error: string) => {
      if (!live) return;
      setRead({ appId: app.id, attempt, list, error });
      onSettled(app.id);
    };
    getApp(app.id).then(
      // An unreadable recipe comes back as an empty list plus an error:
      // that is not "no backup is set up".
      (d) => settle(d.backups ?? [], d.backups_error ? `Backups can't be read right now: ${d.backups_error}` : ""),
      (e) => settle(null, e instanceof Error ? e.message : "Couldn't read the backups"),
    );
    return () => {
      live = false;
    };
  }, [app.id, getApp, attempt, onSettled]);

  const mine = read?.appId === app.id ? read : null;
  const list = mine?.list ?? null;
  const error = mine?.attempt === attempt ? mine.error : "";
  const reported = backupsByApp?.[app.id] ?? [];
  const restoring = restoringBackup(activity);
  const set = (list ?? []).filter((b) => b.storage);
  const backupsTab = toServer(appSub(app.id, "backups"));

  let body: ReactNode;
  if (error) {
    body = <Retry message={error} onRetry={() => setAttempt((n) => n + 1)} />;
  } else if (list === null) {
    body = <Spinner aria-label="Loading backups" />;
  } else if (set.length === 0) {
    body = (
      <div className="space-y-2">
        <p className="text-sm text-muted-foreground">No backup is set up yet.</p>
        <Button asChild size="sm" variant="outline">
          <Link to={backupsTab}>Set up a backup</Link>
        </Button>
      </div>
    );
  } else {
    body = (
      <ul aria-label="Backups" className="divide-y">
        {set.map((b) => {
          const health = shownHealth(overlays[b.name], reported.find((h) => h.name === b.name));
          const { state, restoring: restoringRow, running, note } = backupRowState(b, health, activity[b.name] ?? []);
          const builtin = b.type !== "custom";
          const meta = [health?.last_success ? `Last copy ${relativeTime(new Date(health.last_success * 1000))}` : "", note]
            .filter(Boolean)
            .join(" · ");
          return (
            <li key={b.name} className="flex flex-wrap items-center gap-x-3 gap-y-1 py-2 first:pt-0 last:pb-0">
              <span className="font-mono text-sm">{b.name}</span>
              <StatusPill status={restoringRow ? RESTORING : backupStatus(state)} />
              {meta ? <span className="text-xs text-muted-foreground">{meta}</span> : null}
              {builtin ? (
                <span className="ml-auto flex gap-1">
                  <Button
                    size="sm"
                    variant="ghost"
                    aria-label={`Back up now: ${b.name}`}
                    disabled={!b.enabled || running || !!restoring}
                    onClick={() => void runOne(b.name)}
                  >
                    Back up now
                  </Button>
                  <Button
                    size="sm"
                    variant="ghost"
                    aria-label={`Restore ${b.name}`}
                    disabled={!!restoring}
                    onClick={(e) => {
                      opener.current = e.currentTarget;
                      setPoints(b);
                    }}
                  >
                    Restore…
                  </Button>
                </span>
              ) : null}
            </li>
          );
        })}
      </ul>
    );
  }

  return (
    <Section
      title="Backups"
      className={CARD}
      actions={
        set.length > 0 ? (
          <Link to={backupsTab} className={LINK}>
            Manage backups <ArrowRight className="size-4" aria-hidden />
          </Link>
        ) : undefined
      }
    >
      {body}
      {points ? (
        <RestorePointsDialog
          appId={app.id}
          backup={points}
          backups={backups}
          open
          running={reported.find((h) => h.name === points.name)?.state === "running"}
          returnFocus={opener}
          onOpenChange={(open) => {
            if (!open) setPoints(null);
          }}
          onRestored={() => setAttempt((n) => n + 1)}
        />
      ) : null}
    </Section>
  );
}

// RecentChangesWidget is the app's last three versions: what changed and
// when, which is live and which is a draft; the rest is in History. The
// history walk waits for `enabled` (the other widgets come first) and is
// kept per version of the app, so coming back to the page doesn't repeat it.
function RecentChangesWidget({ app, enabled, onOpenHistory }: { app: App; enabled: boolean; onOpenHistory: () => void }) {
  const { data, loading, error, reload } = useAppRevisions(app.id, enabled, app.version);
  const top = data?.revisions.slice(0, 3) ?? [];
  let body: ReactNode;
  if (error) {
    body = <Retry message={error} onRetry={() => void reload()} />;
  } else if (!data && (loading || !enabled)) {
    body = <Spinner aria-label="Loading changes" />;
  } else if (!data || top.length === 0) {
    body = <p className="text-sm text-muted-foreground">No changes yet.</p>;
  } else {
    body = (
      <ul aria-label="Recent changes" className="space-y-2">
        {top.map((rev) => {
          const flags = revisionFlags(data, rev.hash);
          return (
            <li key={rev.hash} className="flex min-w-0 items-baseline gap-2 text-sm">
              <span className="truncate" title={`Commit ${rev.hash.slice(0, 8)}`}>
                {rev.subject}
              </span>
              <span className="shrink-0 text-xs text-muted-foreground">· {relativeTime(new Date(rev.timestamp * 1000))}</span>
              <RevisionBadge isDeployed={flags.isDeployed} isUndeployedDraft={flags.isUndeployedDraft} />
            </li>
          );
        })}
      </ul>
    );
  }
  return (
    <Section
      title="Recent changes"
      className={CARD}
      actions={
        <Button size="sm" variant="ghost" onClick={onOpenHistory}>
          View history
        </Button>
      }
    >
      {body}
    </Section>
  );
}

// AppOverview is the app page's default tab: status, resources and recent
// changes beside access and backups, at a glance (two independent columns
// from lg, one below).
export function AppOverview({
  app,
  status,
  registrations = [],
  onOpenHistory,
}: {
  app: App;
  status: number | undefined;
  registrations?: RegisteredDomain[];
  onOpenHistory: () => void;
}) {
  // The app whose backups read has ended: the history is read after it.
  const [settledFor, setSettledFor] = useState<string | null>(null);
  return (
    <div className="grid items-start gap-4 lg:grid-cols-2">
      <div className="min-w-0 space-y-4">
        <StatusWidget app={app} status={status} />
        <ResourcesWidget app={app} />
        <RecentChangesWidget app={app} enabled={settledFor === app.id} onOpenHistory={onOpenHistory} />
      </div>
      <div className="min-w-0 space-y-4">
        <AccessWidget app={app} registrations={registrations} />
        <BackupsWidget app={app} onSettled={setSettledFor} />
      </div>
    </div>
  );
}
