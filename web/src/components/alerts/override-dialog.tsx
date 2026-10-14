import { useState } from "react";
import { toast } from "sonner";

import { ResponsiveDialog } from "@/components/responsive-dialog";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import { Switch } from "@/components/ui/switch";
import { alertsPost } from "@/hooks/use-alerts";
import { hasThreshold, metricInfo, routingText, ruleSummary, type Channel, type Override, type Rule } from "@/lib/alerts";

type Props = {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  rule: Rule;
  override?: Override;
  channels: Channel[];
  serverId: string;
  appId: string;
  // targetLabel names the server or app ("prod-1", "nextcloud").
  targetLabel: string;
  onSaved: () => void;
};

type Notify = "inherit" | "specific" | "none";

const FORM_ID = "override-form";

// OverrideDialog adjusts one rule for one server or app, in the rule
// dialog's sentence: switch it off here, change its threshold or duration,
// or send elsewhere. Empty fields keep the rule's value; "Reset to rule"
// removes the adjustment.
export function OverrideDialog({ open, onOpenChange, rule, override, channels, serverId, appId, targetLabel, onSaved }: Props) {
  const info = metricInfo(rule.target_kind, rule.metric);
  const unit = info?.unit === "load" ? "" : (info?.unit ?? "");
  const [on, setOn] = useState(!override?.disabled);
  const [threshold, setThreshold] = useState(override?.threshold != null ? String(override.threshold) : "");
  const [minutes, setMinutes] = useState(override?.duration_minutes != null ? String(override.duration_minutes) : "");
  const initialNotify: Notify =
    override?.channel_ids == null ? "inherit" : override.channel_ids.length === 0 ? "none" : "specific";
  const [notify, setNotify] = useState<Notify>(initialNotify);
  const [picked, setPicked] = useState<string[]>(override?.channel_ids ?? []);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const target = { server_id: serverId, app_id: appId };

  const save = async () => {
    let t: number | null = null;
    if (hasThreshold(rule.metric) && threshold.trim() !== "") {
      t = Number(threshold);
      if (!Number.isFinite(t) || t <= 0 || (info!.max > 0 && t > info!.max)) {
        setError(info!.max > 0 ? `Threshold must be above 0 and at most ${info!.max}` : "Threshold must be above 0");
        return;
      }
    }
    let d: number | null = null;
    if (minutes.trim() !== "") {
      d = Number(minutes);
      if (!Number.isInteger(d) || d < 1 || d > 60) {
        setError("Duration must be 1 to 60 minutes");
        return;
      }
    }
    const channelIds = notify === "inherit" ? null : notify === "none" ? [] : picked;
    setBusy(true);
    try {
      await alertsPost("save-override", {
        rule_id: rule.id,
        ...target,
        disabled: !on,
        threshold: t,
        duration_minutes: d,
        channel_ids: channelIds,
      });
      toast.success("Override saved");
      onSaved();
      onOpenChange(false);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Save failed");
    } finally {
      setBusy(false);
    }
  };

  const reset = async () => {
    setBusy(true);
    try {
      await alertsPost("delete-override", { rule_id: rule.id, ...target });
      toast.success("Back to the rule's settings");
      onSaved();
      onOpenChange(false);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Reset failed");
    } finally {
      setBusy(false);
    }
  };

  return (
    <ResponsiveDialog
      open={open}
      onOpenChange={onOpenChange}
      title={`Adjust for ${targetLabel}`}
      description={`Rule: ${ruleSummary(rule)}, ${routingText(rule.channel_ids, channels).toLowerCase()}. Empty fields keep the rule's values.`}
      className="sm:max-w-lg"
      footer={
        <>
          {override ? (
            <Button type="button" variant="ghost" className="sm:mr-auto" onClick={() => void reset()} disabled={busy}>
              Reset to rule
            </Button>
          ) : null}
          <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button type="submit" form={FORM_ID} disabled={busy}>
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
        <div className="flex items-center justify-between">
          <Label htmlFor="override-on">Alert on {targetLabel}</Label>
          <Switch id="override-on" checked={on} onCheckedChange={setOn} />
        </div>
        <div className="flex flex-wrap items-center gap-2 text-sm">
          <span>Alert when it</span>
          {hasThreshold(rule.metric) ? (
            <>
              <span>is above</span>
              <Input
                id="override-threshold"
                aria-label={`Above (${info?.unit})`}
                className="h-9 w-20 max-sm:h-10"
                type="number"
                step="any"
                placeholder={String(rule.threshold)}
                value={threshold}
                disabled={!on}
                onChange={(e) => setThreshold(e.target.value)}
              />
              {unit ? <span>{unit}</span> : null}
            </>
          ) : (
            <span>is down</span>
          )}
          <span>for</span>
          <Input
            id="override-minutes"
            aria-label="For (minutes)"
            className="h-9 w-16 max-sm:h-10"
            type="number"
            min={1}
            max={60}
            placeholder={String(rule.duration_minutes)}
            value={minutes}
            disabled={!on}
            onChange={(e) => setMinutes(e.target.value)}
          />
          <span>minutes</span>
        </div>
        <div className="space-y-2">
          <p className="text-sm font-medium">Then notify</p>
          <RadioGroup aria-label="Notify" value={notify} onValueChange={(v) => setNotify(v as Notify)} disabled={!on}>
            {(
              [
                ["inherit", "Same as the rule"],
                ["specific", "Specific channels"],
                ["none", "Inbox only"],
              ] as const
            ).map(([v, label]) => (
              <div key={v} className="flex items-center gap-2">
                <RadioGroupItem value={v} id={`override-${v}`} />
                <Label htmlFor={`override-${v}`} className="font-normal">
                  {label}
                </Label>
              </div>
            ))}
          </RadioGroup>
          {notify === "specific" ? (
            <div className="grid gap-1.5 pl-6">
              {channels.map((c) => (
                <div key={c.id} className="flex items-center gap-2">
                  <Checkbox
                    id={`override-ch-${c.id}`}
                    checked={picked.includes(c.id)}
                    onCheckedChange={(checked) => setPicked((p) => (checked ? [...p, c.id] : p.filter((id) => id !== c.id)))}
                  />
                  <Label htmlFor={`override-ch-${c.id}`} className="font-normal">
                    {c.name}
                  </Label>
                </div>
              ))}
            </div>
          ) : null}
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
