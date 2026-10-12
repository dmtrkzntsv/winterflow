// Server-scoped pages live under /server/:serverId so the URL alone says which
// server a tab is looking at: several tabs can work on different servers, and
// links/bookmarks/reloads land on the same server.
export const serverRoutePattern = "/server/:serverId/*";

// serverPath builds an absolute path inside a server's scope; sub is either
// empty (the server's dashboard) or starts with "/".
export function serverPath(serverId: string, sub = ""): string {
  return `/server/${encodeURIComponent(serverId)}${sub}`;
}
