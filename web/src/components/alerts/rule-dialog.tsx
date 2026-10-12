import { useState } from "react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { ChannelPicker } from "@/components/alerts/channel-picker";
import { alertsPost } from "@/hooks/use-alerts";
import {
  ALERT_METRICS,
  hasThreshold,
  metricInfo,
  type AlertMetric,
  type Channel,
  type Rule,
  type TargetKind,
} from "@/lib/alerts";

type Props = {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  rule?: Rule;
  channels: Channel[];
  onSaved: () => void;
};

// RuleDialog creates or edits an org-wide alert rule. A rule's target kind
// and metric are fixed once created.
export function RuleDialog({ open, onOpenChange, rule, channels, onSaved }: Props) {
  const editing = rule !== undefined;
  const [kind, setKind] = useState<TargetKind>(rule?.target_kind ?? "server");
  const [metric, setMetric] = useState<AlertMetric>(rule?.metric ?? "cpu");
  const [threshold, setThreshold] = useState(String(rule?.threshold ?? metricInfo("server", "cpu")!.suggested));
  const [minutes, setMinutes] = useState(String(rule?.duration_minutes ?? 10));
  const [channelIds, setChannelIds] = useState<string[] | null>(rule?.channel_ids ?? null);
  const [enabled, setEnabled] = useState(rule?.enabled ?? true);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const info = metricInfo(kind, metric);
  const pick = (k: TargetKind, m: AlertMetric) => {
    const next = metricInfo(k, m) ?? metricInfo(k, "down")!;
    setKind(k);
    setMetric(next.metric);
    setThreshold(String(next.suggested));
  };

  const save = async () => {
    const t = Number(threshold);
    const d = Number(minutes);
    if (!Number.isInteger(d) || d < 1 || d > 60) {
      setError("Duration must be 1 to 60 minutes");
      return;
    }
    if (hasThreshold(metric) && (!Number.isFinite(t) || t <= 0 || (info!.max > 0 && t > info!.max))) {
      setError(info!.max > 0 ? `Threshold must be above 0 and at most ${info!.max}` : "Threshold must be above 0");
      return;
    }
    setBusy(true);
    try {
      await alertsPost("save-rule", {
        id: rule?.id ?? "",
        target_kind: kind,
        metric,
        threshold: hasThreshold(metric) ? t : 0,
        duration_minutes: d,
        channel_ids: channelIds,
        enabled,
      });
      toast.success(editing ? "Rule updated" : "Rule added");
      onSaved();
      onOpenChange(false);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Save failed");
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{editing ? "Edit rule" : "Add rule"}</DialogTitle>
          <DialogDescription>
            Applies to every {kind === "app" ? "app" : "server"} of the organization; a server or app can override it.
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-4">
          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-1.5">
              <Label htmlFor="rule-kind">Watch</Label>
              <Select value={kind} onValueChange={(v) => pick(v as TargetKind, metric)} disabled={editing}>
                <SelectTrigger id="rule-kind" aria-label="Watch">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="server">Servers</SelectItem>
                  <SelectItem value="app">Apps</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="rule-metric">Metric</Label>
              <Select value={metric} onValueChange={(v) => pick(kind, v as AlertMetric)} disabled={editing}>
                <SelectTrigger id="rule-metric" aria-label="Metric">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {ALERT_METRICS.filter((m) => m.kind === kind).map((m) => (
                    <SelectItem key={m.metric} value={m.metric}>
                      {m.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          </div>
          <div className="grid grid-cols-2 gap-3">
            {hasThreshold(metric) ? (
              <div className="space-y-1.5">
                <Label htmlFor="rule-threshold">Above ({info?.unit === "load" ? "load" : info?.unit})</Label>
                <Input
                  id="rule-threshold"
                  type="number"
                  inputMode="decimal"
                  min={0}
                  max={info?.max || undefined}
                  step="any"
                  value={threshold}
                  onChange={(e) => setThreshold(e.target.value)}
                />
              </div>
            ) : null}
            <div className="space-y-1.5">
              <Label htmlFor="rule-minutes">For (minutes)</Label>
              <Input
                id="rule-minutes"
                type="number"
                min={1}
                max={60}
                value={minutes}
                onChange={(e) => setMinutes(e.target.value)}
              />
            </div>
          </div>
          <div className="space-y-1.5">
            <Label>Notify</Label>
            <ChannelPicker value={channelIds} onChange={setChannelIds} channels={channels} idPrefix="rule" />
          </div>
          <div className="flex items-center justify-between">
            <Label htmlFor="rule-enabled">Enabled</Label>
            <Switch id="rule-enabled" checked={enabled} onCheckedChange={setEnabled} />
          </div>
          {error ? (
            <p role="alert" className="text-sm text-destructive">
              {error}
            </p>
          ) : null}
        </div>
        <DialogFooter>
          <Button onClick={() => void save()} disabled={busy}>
            {busy ? "Saving…" : "Save"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
