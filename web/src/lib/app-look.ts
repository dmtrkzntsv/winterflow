// An app's tile (icon + colour) picked from its name while the user hasn't
// chosen one: a word in the name suggests the icon, the name's hash the
// colour, so the same name always gets the same tile.

export type AppLook = { icon: string; color: string };

// DEFAULT_APP_COLOR is the neutral tile (no name yet).
export const DEFAULT_APP_COLOR = "#64748b";

// APP_COLORS are the tile colours offered and picked from: calm hues that
// read on light and dark.
export const APP_COLORS: readonly string[] = [
  "#0ea5e9",
  "#6366f1",
  "#14b8a6",
  "#22c55e",
  "#f59e0b",
  "#ef4444",
  "#ec4899",
  "#64748b",
];

// COLOR_NAMES name the palette's colours in plain words (for assistive
// technology and tooltips); any other colour is "Custom".
const COLOR_NAMES: Readonly<Record<string, string>> = {
  "#0ea5e9": "Sky",
  "#6366f1": "Indigo",
  "#14b8a6": "Teal",
  "#22c55e": "Green",
  "#f59e0b": "Amber",
  "#ef4444": "Red",
  "#ec4899": "Pink",
  "#64748b": "Slate",
};

export function colorName(color: string): string {
  return COLOR_NAMES[color.toLowerCase()] ?? "Custom";
}

// KEYWORDS: a word of the name starting with one of `starts`, or equal to
// one of `exact` (short stems that would otherwise match unrelated words:
// "doc" is not "docker", "dev" not "devices", "stat" not "static"),
// suggests the icon (names from APP_ICONS).
const KEYWORDS: readonly { starts: readonly string[]; exact?: readonly string[]; icon: string }[] = [
  { starts: ["blog", "wiki", "note", "book"], exact: ["doc", "docs"], icon: "Book" },
  { starts: ["shop", "store", "cart"], icon: "ShoppingCart" },
  { starts: ["db", "database", "postgres", "mysql", "maria", "redis", "mongo", "sql"], icon: "Database" },
  { starts: ["mail", "smtp"], icon: "Mail" },
  { starts: ["photo", "image", "gallery", "immich"], icon: "Camera" },
  { starts: ["video", "jellyfin", "plex", "media", "film", "movie"], icon: "Film" },
  { starts: ["music", "audio", "navidrome", "radio"], icon: "Music" },
  { starts: ["chat", "matrix", "talk"], icon: "MessageSquare" },
  { starts: ["monitor", "grafana", "metric", "analytic", "uptime"], exact: ["stat", "stats"], icon: "Activity" },
  { starts: ["cloud", "drive", "file", "nextcloud", "sync"], icon: "Cloud" },
  { starts: ["git", "code"], exact: ["dev"], icon: "Code" },
  { starts: ["home", "assistant"], icon: "Home" },
  { starts: ["vpn", "proxy", "auth", "vault", "password"], icon: "Shield" },
  { starts: ["web", "site", "www", "nginx"], icon: "Globe" },
];

// hash is FNV-1a (32 bit): stable across sessions and browsers.
function hash(s: string): number {
  let h = 0x811c9dc5;
  for (let i = 0; i < s.length; i++) {
    h ^= s.charCodeAt(i);
    h = Math.imul(h, 0x01000193);
  }
  return h >>> 0;
}

export function suggestAppLook(name: string): AppLook {
  const key = name.trim().toLowerCase();
  if (!key) return { icon: "", color: DEFAULT_APP_COLOR };
  const words = key.split(/[^a-z0-9]+/).filter(Boolean);
  const icon =
    KEYWORDS.find(({ starts, exact = [] }) =>
      words.some((w) => exact.includes(w) || starts.some((s) => w.startsWith(s))),
    )?.icon ?? "";
  return { icon, color: APP_COLORS[hash(key) % APP_COLORS.length] };
}
