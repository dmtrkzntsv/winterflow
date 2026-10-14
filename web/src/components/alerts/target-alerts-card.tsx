import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { SlidersHorizontal } from "lucide-react";

import { OverrideDialog } from "@/components/alerts/override-dialog";
import { Disclosure } from "@/components/disclosure";
import { StatusPill } from "@/components/status";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { alertsGet, useActiveAlerts } from "@/hooks/use-alerts";
import {
  conditionText,
  effective,
  hasThreshold,
  metricInfo,
  type Channel,
  type Override,
  type Rule,
  type TargetKind,
} from "@/lib/alerts";
import { settingsPath } from "@/lib/routes";
import type { StatusInfo } from "@/lib/status";
import { cn } from "@/lib/utils";

type Props = {
  serverId: string;
  // appId targets one app; empty or absent targets the server itself.
  appId?: string;
  // targetLabel names the target in dialogs ("prod-1", "nextcloud").
  targetLabel: string;
  // defaultOpen starts the section expanded (collapsed by default).
  defaultOpen?: boolean;
};

const FIRING: StatusInfo = { tone: "bad", label: "Firing" };

// TargetAlertsCard is "Alert rules for this server/app", collapsed under a
// server's Monitoring charts and an app's Metrics tab (spec §5). Opened, it
// lists every org rule of the target's kind with its effective setting here,
// adjustable per target (overrides). The rules are read only once it is
// opened: a collapsed section costs nothing.
export function TargetAlertsCard({ serverId, appId = "", targetLabel, defaultOpen = false }: Props) {
  const kind: TargetKind = appId ? "app" : "server";
  return (
    <section aria-label="Alert rules" className="relative">
      <Link
        to={settingsPath("notifications")}
        className="absolute top-0 right-0 inline-flex min-h-10 items-center text-xs text-muted-foreground transition-colors duration-150 hover:text-foreground hover:underline"
      >
        Manage rules
      </Link>
      <Disclosure label={`Alert rules for this ${kind}`} defaultOpen={defaultOpen}>
        <TargetRules serverId={serverId} appId={appId} kind={kind} targetLabel={targetLabel} />
      </Disclosure>
    </section>
  );
}

// TargetRules is the opened section's body; it mounts (and fetches) only
// while the Disclosure is open.
function TargetRules({
  serverId,
  appId,
  kind,
  targetLabel,
}: {
  serverId: string;
  appId: string;
  kind: TargetKind;
  targetLabel: string;
}) {
  const { data: active } = useActiveAlerts(serverId, appId || null);
  const firing = new Set(active.filter((a) => a.app_id === appId).map((a) => a.rule_id));
  const [rules, setRules] = useState<Rule[]>([]);
  const [overrides, setOverrides] = useState<Override[]>([]);
  const [channels, setChannels] = useState<Channel[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [adjusting, setAdjusting] = useState<Rule | null>(null);
  // version bumps after an override changes, reloading the rules.
  const [version, setVersion] = useState(0);

  useEffect(() => {
    let current = true;
    const target: Record<string, string> = { server_id: serverId };
    if (appId) target.app_id = appId;
    Promise.all([
      alertsGet<Rule[] | null>("get-rules"),
      alertsGet<Override[] | null>("get-overrides", target),
      alertsGet<Channel[] | null>("get-channels"),
    ])
      .then(([r, o, c]) => {
        if (!current) return;
        setRules((r ?? []).filter((x) => x.target_kind === kind));
        setOverrides(o ?? []);
        setChannels(c ?? []);
        setError(null);
        setLoaded(true);
      })
      .catch((e: unknown) => {
        if (current) setError(e instanceof Error ? e.message : "Failed to load alert rules");
      });
    return () => {
      current = false;
    };
  }, [serverId, appId, kind, version]);

  return (
    <div className="space-y-2">
      {error ? <p className="text-sm text-destructive">{error}</p> : null}
      <ul className="grid gap-1.5" aria-label="Rules">
        {rules.map((r) => {
          const o = overrides.find((x) => x.rule_id === r.id);
          const eff = effective(r, o);
          const info = metricInfo(r.target_kind, r.metric);
          return (
            <li key={r.id} className="flex min-h-10 items-center gap-2 text-sm" data-testid="target-rule">
              <span className={cn("min-w-0 flex-1 truncate", !eff.enabled && "text-muted-foreground line-through")}>
                {hasThreshold(r.metric) ? (
                  <>
                    <span className="font-medium">{info?.label ?? r.metric}</span>{" "}
                    {conditionText(r.metric, info?.unit ?? "", eff.threshold, eff.duration_minutes)}
                  </>
                ) : (
                  <span className="font-medium">Down for {eff.duration_minutes} min</span>
                )}
              </span>
              {firing.has(r.id) ? <StatusPill status={FIRING} /> : null}
              {o ? <Badge variant="secondary">{o.disabled ? "Off here" : "Adjusted"}</Badge> : null}
              <Button
                variant="ghost"
                size="icon"
                className="size-8 max-sm:size-10"
                aria-label={`Adjust ${info?.label ?? r.metric} for ${targetLabel}`}
                onClick={() => setAdjusting(r)}
              >
                <SlidersHorizontal className="size-3.5" />
              </Button>
            </li>
          );
        })}
        {loaded && rules.length === 0 ? (
          <li className="text-sm text-muted-foreground">No {kind} rules yet.</li>
        ) : null}
      </ul>

      {adjusting ? (
        <OverrideDialog
          key={adjusting.id}
          open
          onOpenChange={(open) => !open && setAdjusting(null)}
          rule={adjusting}
          override={overrides.find((x) => x.rule_id === adjusting.id)}
          channels={channels}
          serverId={serverId}
          appId={appId}
          targetLabel={targetLabel}
          onSaved={() => setVersion((v) => v + 1)}
        />
      ) : null}
    </div>
  );
}
