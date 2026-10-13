import { vi } from "vitest";

export type Call = { path: string; query: string; body: unknown };

// fakeAPI answers fetches by path from data (a value, or a function of the
// request body and query) and records every call. Unknown paths answer
// 404; an Error answers 400, or its status when it has one (an ApiError).
export function fakeAPI(data: Record<string, unknown>) {
  const calls: Call[] = [];
  const fetchMock = vi.fn(async (url: string, init?: RequestInit) => {
    const u = new URL(url, "http://x");
    const body = init?.body ? JSON.parse(String(init.body)) : undefined;
    calls.push({ path: u.pathname, query: u.search, body });
    if (!(u.pathname in data)) {
      return new Response(JSON.stringify({ success: false, message: "no route" }), { status: 404 });
    }
    const entry = data[u.pathname];
    const value =
      typeof entry === "function" ? (entry as (b: unknown, q: URLSearchParams) => unknown)(body, u.searchParams) : entry;
    if (value instanceof Error) {
      const status = (value as Error & { status?: number }).status ?? 400;
      return new Response(JSON.stringify({ success: false, message: value.message }), { status });
    }
    return new Response(JSON.stringify({ success: true, message: "ok", data: value }));
  });
  vi.stubGlobal("fetch", fetchMock);
  return { calls, fetchMock, posted: (path: string) => calls.filter((c) => c.path === path).map((c) => c.body) };
}
