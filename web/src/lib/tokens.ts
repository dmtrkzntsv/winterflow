import { relativeTime } from "@/lib/utils";

const date = (iso: string) => new Date(iso).toLocaleDateString();

// expiresIn says, by calendar date, when a token ends: "Expires today" on
// its last date, "Expires tomorrow", else "Expires in 30 days".
function expiresIn(at: number, now: number): string {
  const day = (ms: number) => {
    const d = new Date(ms);
    return new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();
  };
  // Rounded: a day across a clock change is 23 or 25 hours.
  const days = Math.round((day(at) - day(now)) / 86_400_000);
  if (days < 1) return "Expires today";
  return days === 1 ? "Expires tomorrow" : `Expires in ${days} days`;
}

// tokenMeta is a token row's quiet line: when it was made, how long it has
// left (or that it never ends, or when it did), and when it was last used —
// all relative to now, past a week the date.
export function tokenMeta(
  t: { created_at: string; expires_at: string | null; last_used_at: string | null },
  now: number,
): string {
  const ends = !t.expires_at
    ? "Never expires"
    : Date.parse(t.expires_at) <= now
      ? `Expired ${date(t.expires_at)}`
      : expiresIn(Date.parse(t.expires_at), now);
  const used = t.last_used_at ? `Last used ${relativeTime(new Date(t.last_used_at), now)}` : "Never used";
  return [`Created ${relativeTime(new Date(t.created_at), now)}`, ends, used].join(" · ");
}
