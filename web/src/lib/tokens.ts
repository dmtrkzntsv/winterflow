import { relativeTime } from "@/lib/utils";

const date = (iso: string) => new Date(iso).toLocaleDateString();

// tokenMeta is a token row's quiet line: when it was made, when it ends
// (or that it doesn't, or did), and when it was last used.
export function tokenMeta(
  t: { created_at: string; expires_at: string | null; last_used_at: string | null },
  now: number,
): string {
  const ends = !t.expires_at
    ? "Never expires"
    : Date.parse(t.expires_at) <= now
      ? `Expired ${date(t.expires_at)}`
      : `Expires ${date(t.expires_at)}`;
  const used = t.last_used_at ? `Last used ${relativeTime(new Date(t.last_used_at), now)}` : "Never used";
  return [`Created ${date(t.created_at)}`, ends, used].join(" · ");
}
