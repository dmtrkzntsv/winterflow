import { useState } from "react";
import { Loader2 } from "lucide-react";
import { Link } from "react-router-dom";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { ErrorText } from "@/components/error-text";
import { useRollout } from "@/hooks/use-rollout";
import { useRolloutsApi } from "@/hooks/use-rollouts-api";
import { serverPath } from "@/lib/server-paths";
import { currentApp, failureLine, isActive, rolloutLine, skippedLine, type Rollout, type RolloutsApi } from "@/lib/rollouts";

// ReloadRolloutStrip is the org's shared-variable reload rollout as a
// RolloutStrip with Stop (api: a test's client), its servers named by
// serverNames.
export function ReloadRolloutStrip({ api, serverNames }: { api?: RolloutsApi; serverNames: Map<string, string> }) {
  const { rollout, stop } = useRollout(useRolloutsApi(api), "vars_reload");
  return <RolloutStrip rollout={rollout} serverName={(id) => serverNames.get(id) ?? "a server"} onStop={stop} />;
}

// RolloutStrip shows a rollout in progress ("Updating 3 of 12: bakomi…"
// with Stop) or the failure that ended one, naming the app with a link to
// it and the error (Dismiss hides it for this visit); a failed reload adds
// that later reloads stop at the same app (it is picked first again). An
// "Update all apps" that ended (done or stopped) with instances skipped
// shows an info line saying so, dismissible the same way. Nothing for any
// other finished or stopped rollout, or none.
export function RolloutStrip({
  rollout,
  serverName,
  onStop,
}: {
  rollout: Rollout | null;
  serverName: (id: string) => string;
  onStop: (id: string) => Promise<void>;
}) {
  const [stopping, setStopping] = useState(false);
  const [dismissed, setDismissed] = useState<string | null>(null); // an ended rollout's id, failed or with skips

  if (!rollout) return null;
  if (isActive(rollout)) {
    const stop = async () => {
      setStopping(true);
      try {
        await onStop(rollout.id);
      } catch (e) {
        toast.error("Couldn't stop it", { description: e instanceof Error ? e.message : undefined });
      } finally {
        setStopping(false);
      }
    };
    return (
      <div role="status" className="flex flex-wrap items-center gap-3 rounded-md border bg-muted/30 px-4 py-2 text-sm">
        <Loader2 aria-hidden className="size-4 animate-spin text-muted-foreground" />
        <span className="mr-auto">{rolloutLine(rollout, serverName)}</span>
        <Button size="sm" variant="outline" disabled={stopping || rollout.stop_requested} onClick={() => void stop()}>
          Stop
        </Button>
      </div>
    );
  }
  if (dismissed === rollout.id) return null;
  const skipped = skippedLine(rollout);
  if (skipped) {
    return (
      <div role="status" className="flex flex-wrap items-center gap-3 rounded-md border bg-muted/30 px-4 py-2 text-sm">
        <span className="mr-auto">{skipped}</span>
        <Button size="sm" variant="ghost" onClick={() => setDismissed(rollout.id)}>
          Dismiss
        </Button>
      </div>
    );
  }
  if (rollout.state !== "failed") return null;
  const link = rollout.current_app_id ? serverPath(rollout.current_server_id, `/app/${rollout.current_app_id}`) : null;
  return (
    <div role="alert" className="flex flex-wrap items-start gap-3 rounded-md border border-destructive/40 px-4 py-2 text-sm">
      <div className="mr-auto min-w-0 space-y-1">
        <p>
          {failureLine(rollout, serverName)}
          {link ? (
            <>
              {" · "}
              <Link to={link} className="font-medium underline-offset-2 hover:underline">
                Open {currentApp(rollout)}
              </Link>
            </>
          ) : null}
        </p>
        {rollout.error ? <ErrorText text={rollout.error} className="text-xs" /> : null}
        {rollout.kind === "vars_reload" && rollout.current_app_id ? (
          <p className="text-xs text-muted-foreground">Reloads stop at this app until it is fixed or stopped.</p>
        ) : null}
      </div>
      <Button size="sm" variant="ghost" onClick={() => setDismissed(rollout.id)}>
        Dismiss
      </Button>
    </div>
  );
}
