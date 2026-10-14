import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { Flame, SlidersHorizontal } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { OverrideDialog } from "@/components/alerts/override-dialog";
import { alertsGet, useActiveAlerts } from "@/hooks/use-alerts";
import { useNow } from "@/hooks/use-now";
import {
  activeText,
  conditionText,
  effective,
  hasThreshold,
  metricInfo,
  sinceText,
  type Channel,
  type Override,
  type Rule,
} from "@/lib/alerts";
import { cn } from "@/lib/utils";
import { settingsPath } from "@/lib/routes";

type Props = {
  serverId: string;
  // appId targets one app; empty or absent targets the server itself.
  appId?: string;
  // targetLabel names the target in dialogs ("prod-1", "nextcloud").
  targetLabel: string;
};

// TargetAlertsCard is the Alerts card of a server's Monitoring page or an
// app's Metrics tab: what is firing here now, and every org rule that
// applies with its effective setting for this target, adjustable per
// target (overrides).
export function TargetAlertsCard({ serverId, appId = "", targetLabel }: Props) {
  const kind = appId ? "app" : "server";
  const now = useNow();
  const { data: active } = useActiveAlerts(serverId, appId || null);
  const firing = active.filter((a) => a.app_id === appId);
  const [rules, setRules] = useState<Rule[]>([]);
  const [overrides, setOverrides] = useState<Override[]>([]);
  const [channels, setChannels] = useState<Channel[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [adjusting, setAdjusting] = useState<Rule | null>(null);

  // version bumps after an override changes, reloading the card.
  const [version, setVersion] = useState(0);

  useEffect(() => {
    let active = true;
    const target: Record<string, string> = { server_id: serverId };
    if (appId) target.app_id = appId;
    Promise.all([
      alertsGet<Rule[] | null>("get-rules"),
      alertsGet<Override[] | null>("get-overrides", target),
      alertsGet<Channel[] | null>("get-channels"),
    ])
      .then(([r, o, c]) => {
        if (!active) return;
        setRules((r ?? []).filter((x) => x.target_kind === kind));
        setOverrides(o ?? []);
        setChannels(c ?? []);
        setError(null);
      })
      .catch((e: unknown) => {
        if (active) setError(e instanceof Error ? e.message : "Failed to load alerts");
      });
    return () => {
      active = false;
    };
  }, [serverId, appId, kind, version]);

  return (
    <Card data-slot="widget-card" className="flex min-w-0 flex-col gap-3 p-3.5" role="region" aria-label="Alerts">
      <div className="flex items-center justify-between gap-3">
        <h2 className="text-sm font-medium">Alerts</h2>
        <Link to={settingsPath("notifications")} className="text-xs text-muted-foreground hover:text-foreground hover:underline">
          Manage rules
        </Link>
      </div>
      {error ? <p className="text-sm text-destructive">{error}</p> : null}

      {firing.length > 0 ? (
        <ul className="grid gap-1" aria-label="Firing">
          {firing.map((a) => (
            <li key={a.rule_id} className="flex items-center gap-2 text-sm text-destructive">
              <Flame className="size-4 shrink-0" aria-hidden />
              <span className="truncate font-medium">{activeText(a)}</span>
              <span className="ml-auto shrink-0 text-xs tabular-nums">for {sinceText(a.since, now)}</span>
            </li>
          ))}
        </ul>
      ) : (
        <p className="text-sm text-muted-foreground">Nothing firing.</p>
      )}

      <ul className="grid gap-1.5" aria-label="Rules">
        {rules.map((r) => {
          const o = overrides.find((x) => x.rule_id === r.id);
          const eff = effective(r, o);
          const info = metricInfo(r.target_kind, r.metric);
          return (
            <li key={r.id} className="flex items-center gap-2 text-sm" data-testid="target-rule">
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
              {o ? <Badge variant="secondary">{o.disabled ? "Off here" : "Adjusted"}</Badge> : null}
              <Button
                variant="ghost"
                size="icon"
                className="size-7"
                aria-label={`Adjust ${info?.label ?? r.metric} for ${targetLabel}`}
                onClick={() => setAdjusting(r)}
              >
                <SlidersHorizontal className="size-3.5" />
              </Button>
            </li>
          );
        })}
        {rules.length === 0 && !error ? (
          <li className="text-sm text-muted-foreground">No {kind} rules in this organization.</li>
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
    </Card>
  );
}
