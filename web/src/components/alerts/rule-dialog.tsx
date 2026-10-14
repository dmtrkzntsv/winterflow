import { useState, type RefObject } from "react";
import { toast } from "sonner";

import { ChannelPicker } from "@/components/alerts/channel-picker";
import { ResponsiveDialog } from "@/components/responsive-dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
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
import { returnFocusTo } from "@/lib/focus";

type Props = {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  rule?: Rule;
  channels: Channel[];
  onSaved: () => void;
  // returnFocus is where focus goes back on close (the row it was opened
  // from may be gone).
  returnFocus?: RefObject<HTMLElement | null>;
};

const FORM_ID = "rule-form";

// RuleDialog creates or edits an org-wide alert rule, written as one
// sentence: "Alert when [a server] [CPU] is above [90] % for [10] minutes",
// then where to notify. A rule's target kind and metric are fixed once
// created.
export function RuleDialog({ open, onOpenChange, rule, channels, onSaved, returnFocus }: Props) {
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
  // The unit after the number ("%", "GB"); a load has none.
  const unit = info?.unit === "load" ? "" : (info?.unit ?? "");

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
    <ResponsiveDialog
      open={open}
      onOpenChange={onOpenChange}
      title={editing ? "Edit rule" : "Add rule"}
      description={`Applies to every ${kind === "app" ? "app" : "server"} in the organization; each can adjust it on its own page.`}
      className="sm:max-w-lg"
      onCloseAutoFocus={returnFocusTo(returnFocus)}
      footer={
        <>
          <Button type="button" variant="outline" className="max-sm:h-10" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button type="submit" form={FORM_ID} className="max-sm:h-10" disabled={busy}>
            {busy ? "Saving…" : "Save"}
          </Button>
        </>
      }
    >
      <form
        id={FORM_ID}
        noValidate
        className="space-y-5"
        onSubmit={(e) => {
          e.preventDefault();
          void save();
        }}
      >
        <div className="flex flex-wrap items-center gap-2 text-sm">
          <span>Alert when</span>
          <Select value={kind} onValueChange={(v) => pick(v as TargetKind, metric)} disabled={editing}>
            <SelectTrigger aria-label="Watch" className="h-9 w-auto min-w-28 max-sm:h-10">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="server">a server</SelectItem>
              <SelectItem value="app">an app</SelectItem>
            </SelectContent>
          </Select>
          <Select value={metric} onValueChange={(v) => pick(kind, v as AlertMetric)} disabled={editing}>
            <SelectTrigger aria-label="Metric" className="h-9 w-auto min-w-28 max-sm:h-10">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {ALERT_METRICS.filter((m) => m.kind === kind).map((m) => (
                <SelectItem key={m.metric} value={m.metric}>
                  {hasThreshold(m.metric) ? m.label : "is down"}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          {hasThreshold(metric) ? (
            <>
              <span>is above</span>
              <Input
                aria-label={`Above (${info?.unit === "load" ? "load" : info?.unit})`}
                className="h-9 w-20 max-sm:h-10"
                type="number"
                inputMode="decimal"
                min={0}
                max={info?.max || undefined}
                step="any"
                value={threshold}
                onChange={(e) => setThreshold(e.target.value)}
              />
              {unit ? <span>{unit}</span> : null}
            </>
          ) : null}
          <span>for</span>
          <Input
            aria-label="For (minutes)"
            className="h-9 w-16 max-sm:h-10"
            type="number"
            min={1}
            max={60}
            value={minutes}
            onChange={(e) => setMinutes(e.target.value)}
          />
          <span>minutes</span>
        </div>
        <div className="space-y-1.5">
          <p className="text-sm font-medium">Then notify</p>
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
      </form>
    </ResponsiveDialog>
  );
}
