import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { NativeSelect } from "@/components/native-select";
import { MANUAL, SCHEDULES, type BackupType } from "@/lib/backups";

// ScheduleSelect picks how often a backup runs. A value the list lacks (a
// schedule typed in the compose file) is kept as its own choice.
export function ScheduleSelect({
  value,
  onChange,
  id,
  "aria-label": ariaLabel,
}: {
  value: string;
  onChange: (value: string) => void;
  id?: string;
  "aria-label"?: string;
}) {
  const options = SCHEDULES.some((s) => s.value === value) ? SCHEDULES : [...SCHEDULES, { value, label: `Every ${value}` }];
  return (
    <NativeSelect id={id} aria-label={ariaLabel} value={value} onChange={(e) => onChange(e.target.value)}>
      {options.map((s) => (
        <option key={s.value} value={s.value}>
          {s.value === MANUAL ? "Manual (Back up now only)" : s.label}
        </option>
      ))}
    </NativeSelect>
  );
}

// PathsField is the paths of a backup: one file for a sqlite database, a
// list (one per line) for folders and files. compact is the edit dialog's
// smaller label, which also says what the paths are relative to.
export function PathsField({
  id,
  type,
  where,
  value,
  onChange,
  compact,
}: {
  id: string;
  type: BackupType;
  where: "volume" | "service";
  value: string;
  onChange: (value: string) => void;
  compact?: boolean;
}) {
  const label = (type === "sqlite" ? "Database file" : "Paths, one per line") + (compact ? (where === "volume" ? " (inside the volume)" : " (in the container)") : "");
  return (
    <div className={compact ? "grid gap-1" : "grid gap-1.5"}>
      <Label htmlFor={id} className={compact ? "text-xs font-normal text-muted-foreground" : undefined}>
        {label}
      </Label>
      <Textarea
        id={id}
        rows={type === "sqlite" ? 1 : 3}
        className="font-mono text-sm"
        placeholder={where === "volume" ? (type === "sqlite" ? "app.db" : "empty: the whole volume") : "/app/data"}
        value={value}
        onChange={(e) => onChange(e.target.value)}
      />
    </div>
  );
}
