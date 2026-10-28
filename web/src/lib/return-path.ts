// safePath is path when it is a same-origin relative path, else null: one
// leading "/" not followed by another "/" or a "\" (either makes a
// protocol-relative URL that leaves the site), and no control characters
// (URL parsers drop tabs and newlines, so "/\t/x" would read as "//x").
export function safePath(path: unknown): string | null {
  if (typeof path !== "string") return null;
  // eslint-disable-next-line no-control-regex
  if (!/^\/(?![/\\])/.test(path) || /[\u0000-\u001f\u007f]/.test(path)) return null;
  return path;
}

export type ReturnFrom = { pathname?: unknown; search?: unknown; hash?: unknown };

// fromLocation is where a location points, query and hash included, or null
// when that is not a safe same-origin path. The query must start with "?"
// and the hash with "#" (or be empty); the parts are checked apart and
// joined: "/" with a query of "/evil.example" would otherwise join into
// "//evil.example".
export function fromLocation(from: ReturnFrom | undefined): string | null {
  const pathname = from?.pathname;
  const search = from?.search ?? "";
  const hash = from?.hash ?? "";
  if (typeof search !== "string" || typeof hash !== "string") return null;
  if ((search !== "" && !search.startsWith("?")) || (hash !== "" && !hash.startsWith("#"))) return null;
  if (safePath(pathname) === null) return null;
  return safePath(`${pathname as string}${search}${hash}`);
}

// loginPath is the sign-in page that comes back to next afterwards (plain
// /login for home or an unsafe next).
export function loginPath(next: string): string {
  const safe = safePath(next);
  return safe && safe !== "/" ? `/login?next=${encodeURIComponent(safe)}` : "/login";
}

// returnTarget is where a sign-in goes when it is done: the login URL's
// ?next=, else the location the guard sent along, else home.
export function returnTarget(search: URLSearchParams, from: ReturnFrom | undefined): string {
  if (search.has("next")) return safePath(search.get("next")) ?? "/";
  return fromLocation(from) ?? "/";
}
