import { Checkbox } from "@/components/ui/checkbox";
import { Label } from "@/components/ui/label";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import { routingOf, type Channel } from "@/lib/alerts";

type Props = {
  // value: null = default channels, [] = the Inbox only, else those ids.
  value: string[] | null;
  onChange: (ids: string[] | null) => void;
  channels: Channel[];
  // idPrefix keeps element ids unique when two pickers share a page.
  idPrefix: string;
};

// ChannelPicker chooses where notifications go: the org's default channels,
// specific channels, or none (the alert only shows in the Inbox).
export function ChannelPicker({ value, onChange, channels, idPrefix }: Props) {
  const routing = routingOf(value);
  const selected = value ?? [];
  return (
    <div className="space-y-2">
      <RadioGroup
        aria-label="Notify"
        value={routing}
        onValueChange={(v) => {
          if (v === "default") onChange(null);
          else if (v === "none") onChange([]);
          else onChange(channels.filter((c) => c.enabled).slice(0, 1).map((c) => c.id));
        }}
      >
        {(
          [
            ["default", "Default channels"],
            ["specific", "Specific channels"],
            ["none", "Inbox only"],
          ] as const
        ).map(([v, label]) => (
          <div key={v} className="flex items-center gap-2">
            <RadioGroupItem value={v} id={`${idPrefix}-${v}`} />
            <Label htmlFor={`${idPrefix}-${v}`} className="font-normal">
              {label}
            </Label>
          </div>
        ))}
      </RadioGroup>
      {routing === "specific" ? (
        channels.length === 0 ? (
          <p className="text-xs text-muted-foreground">No channels yet: add one under Channels.</p>
        ) : (
          <div className="grid gap-1.5 pl-6">
            {channels.map((c) => (
              <div key={c.id} className="flex items-center gap-2">
                <Checkbox
                  id={`${idPrefix}-ch-${c.id}`}
                  checked={selected.includes(c.id)}
                  onCheckedChange={(checked) =>
                    onChange(checked ? [...selected, c.id] : selected.filter((id) => id !== c.id))
                  }
                />
                <Label htmlFor={`${idPrefix}-ch-${c.id}`} className="font-normal">
                  {c.name}
                  {!c.enabled ? <span className="text-muted-foreground"> (disabled)</span> : null}
                </Label>
              </div>
            ))}
          </div>
        )
      ) : null}
    </div>
  );
}
