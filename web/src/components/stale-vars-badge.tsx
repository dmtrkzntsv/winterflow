import { useEffect, useState } from "react";
import { RefreshCcwDot, RotateCw } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { useApps } from "@/context/use-apps";
import { useServers } from "@/context/use-servers";
import { useNotifications } from "@/context/use-notifications";
import { hasRunningContainers } from "@/lib/app-status";
import type { VarsReloaded } from "@/lib/shared-variables";

// The agent reports nothing for an app it skipped (e.g. stopped meanwhile),
// so a reload that never reports back must not leave the button busy forever.
const RELOAD_REPORT_TIMEOUT_MS = 2 * 60 * 1000;

// StaleVarsBadge tells that an app's shared variables changed since it was
// last rendered and offers the reload that applies them. The button stays
// busy until the agent reports the outcome (vars.reloaded).
export function StaleVarsBadge({ appId }: { appId: string }) {
  const { staleVarsByApp, statusByApp, reloadSharedVars } = useApps();
  const { subscribe } = useNotifications();
  const { activeServerId } = useServers();
  const [busy, setBusy] = useState(false);
  const names = staleVarsByApp[appId] ?? [];
  const fresh = names.length === 0;

  // A fresh app has nothing left to wait for (adjusted during render, not in
  // an effect).
  const [wasFresh, setWasFresh] = useState(fresh);
  if (wasFresh !== fresh) {
    setWasFresh(fresh);
    if (fresh) setBusy(false);
  }

  // Give up waiting for the outcome after a while.
  useEffect(() => {
    if (!busy) return;
    const t = setTimeout(() => setBusy(false), RELOAD_REPORT_TIMEOUT_MS);
    return () => clearTimeout(t);
  }, [busy]);

  useEffect(
    () =>
      subscribe((n) => {
        if (n.type !== "vars.reloaded") return;
        const p = n.payload as VarsReloaded | undefined;
        if (p?.app_id !== appId || p.server_id !== activeServerId) return;
        setBusy(false);
        if (p.ok) toast.success("Shared variables applied");
        else toast.error("Reload failed", { description: p.error });
      }),
    [subscribe, appId, activeServerId],
  );

  if (fresh) return null;
  // The agent reloads running apps only; a stopped one renders on start.
  const running = hasRunningContainers(statusByApp[appId]);

  const reload = async () => {
    setBusy(true);
    try {
      await reloadSharedVars(appId);
      toast.info("Reload queued");
    } catch (e) {
      setBusy(false);
      toast.error("Couldn't reload the app", { description: e instanceof Error ? e.message : undefined });
    }
  };

  return (
    // The firing banner's look in the warn tone: the text stays foreground,
    // the tone is on the icon and the border.
    <div
      role="status"
      className="flex flex-wrap items-center gap-2 rounded-xl border border-status-warn/40 bg-status-warn/5 px-4 py-2 text-sm text-foreground"
    >
      <RefreshCcwDot className="size-4 shrink-0 text-status-warn" aria-hidden />
      <span className="font-medium">{running ? "Shared variables changed — reload to apply" : "Shared variables changed — applies on next start"}</span>
      <span className="font-mono text-xs text-muted-foreground">{names.join(", ")}</span>
      {running ? (
        <Button size="sm" variant="outline" className="ml-auto" disabled={busy} onClick={() => void reload()}>
          {busy ? <Spinner className="size-4" /> : <RotateCw className="size-4" />}
          Reload
        </Button>
      ) : null}
    </div>
  );
}
