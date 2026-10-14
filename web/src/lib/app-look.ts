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

// KEYWORDS: a word of the name starting with one of these suggests the icon
// (names from APP_ICONS).
const KEYWORDS: readonly [readonly string[], string][] = [
  [["blog", "wiki", "doc", "note", "book"], "Book"],
  [["shop", "store", "cart"], "ShoppingCart"],
  [["db", "database", "postgres", "mysql", "maria", "redis", "mongo", "sql"], "Database"],
  [["mail", "smtp"], "Mail"],
  [["photo", "image", "gallery", "immich"], "Camera"],
  [["video", "jellyfin", "plex", "media", "film", "movie"], "Film"],
  [["music", "audio", "navidrome", "radio"], "Music"],
  [["chat", "matrix", "talk"], "MessageSquare"],
  [["monitor", "grafana", "metric", "stat", "analytic", "uptime"], "Activity"],
  [["cloud", "drive", "file", "nextcloud", "sync"], "Cloud"],
  [["git", "code", "dev"], "Code"],
  [["home", "assistant"], "Home"],
  [["vpn", "proxy", "auth", "vault", "password"], "Shield"],
  [["web", "site", "www", "nginx"], "Globe"],
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
  const icon = KEYWORDS.find(([starts]) => words.some((w) => starts.some((s) => w.startsWith(s))))?.[1] ?? "";
  return { icon, color: APP_COLORS[hash(key) % APP_COLORS.length] };
}
