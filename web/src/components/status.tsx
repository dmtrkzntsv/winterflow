import { TONE_DOT_CLASS, TONE_PILL_CLASS, type StatusInfo, type StatusTone } from "@/lib/status";
import { cn } from "@/lib/utils";

// StatusDot is a state's coloured dot. With a label it is an image named by
// the label (for a dot standing alone); without one it is decoration next
// to a word that says the same.
export function StatusDot({
  tone,
  pulse = false,
  label,
  className,
}: {
  tone: StatusTone;
  pulse?: boolean;
  label?: string;
  className?: string;
}) {
  return (
    <span
      data-tone={tone}
      className={cn(
        "inline-block size-2 shrink-0 rounded-full",
        TONE_DOT_CLASS[tone],
        pulse && "animate-pulse motion-reduce:animate-none",
        className,
      )}
      {...(label ? { role: "img", "aria-label": label, title: label } : { "aria-hidden": true })}
    />
  );
}

// StatusPill is a state as a small tinted pill: dot and word.
export function StatusPill({ status, className }: { status: StatusInfo; className?: string }) {
  return (
    <span
      data-tone={status.tone}
      className={cn(
        "inline-flex items-center gap-1.5 whitespace-nowrap rounded-full px-2.5 py-0.5 text-xs font-medium",
        TONE_PILL_CLASS[status.tone],
        className,
      )}
    >
      <StatusDot tone={status.tone} pulse={status.pulse} />
      {status.label}
    </span>
  );
}
