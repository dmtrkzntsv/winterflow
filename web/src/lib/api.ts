// APP_REQUEST_HEADER marks a request as the web app's own: the API refuses
// a cookie-authenticated write (and a sign-in) without it, which a
// cross-site form or simple cross-origin request cannot set (CSRF).
export const APP_REQUEST_HEADER = "X-Winterflow-Request";

const SAFE_METHODS = new Set(["GET", "HEAD", "OPTIONS"]);

// apiInit is init as every API request needs it: the session cookie
// (credentials "include", for a UI on a listed other origin too) and, on a
// method that changes state, APP_REQUEST_HEADER.
export function apiInit(init: RequestInit = {}): RequestInit {
  const out: RequestInit = { credentials: "include", ...init };
  if (!SAFE_METHODS.has((init.method ?? "GET").toUpperCase())) {
    const headers = new Headers(init.headers);
    headers.set(APP_REQUEST_HEADER, "1");
    out.headers = headers;
  }
  return out;
}

// apiFetch is fetch for the API: every request to it goes through here
// (or createJsonClient / dispatchCommand, which use it), so none can miss
// the cookie or the app-request header.
export function apiFetch(input: string, init?: RequestInit): Promise<Response> {
  return fetch(input, apiInit(init));
}

// readApiError turns a failed API response into an Error carrying the
// server's explanation (the envelope's `message`), falling back to the
// status when the body has none or isn't JSON.
export async function readApiError(res: Response): Promise<Error> {
  const body = (await res.json().catch(() => null)) as { message?: unknown } | null;
  const message = typeof body?.message === "string" ? body.message.trim() : "";
  return new Error(message || `Request failed: ${res.status}`);
}

// ApiError is a refused request with its HTTP status (409: a concurrent
// edit, a key in use, a template with live instances, an active rollout).
export class ApiError extends Error {
  status: number;
  constructor(message: string, status: number) {
    super(message);
    this.status = status;
  }
}

// jsonPost is a POST of payload as JSON.
export function jsonPost(payload: unknown): RequestInit {
  return { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(payload) };
}

export type JsonClient = {
  // request fetches base + path with the session cookie and throws a
  // refusal (non-2xx) as an ApiError carrying the server's message.
  request(path: string, init?: RequestInit): Promise<Response>;
  // call is request's envelope `data`.
  call<T>(path: string, init?: RequestInit): Promise<T>;
};

// createJsonClient is the client of the API's JSON envelope under base
// (fetchImpl: tests).
export function createJsonClient(base: string, fetchImpl?: typeof fetch): JsonClient {
  const doFetch: typeof fetch = fetchImpl ?? ((input, init) => fetch(input, init));
  async function request(path: string, init?: RequestInit): Promise<Response> {
    const res = await doFetch(`${base}${path}`, apiInit(init));
    if (!res.ok) throw new ApiError((await readApiError(res)).message, res.status);
    return res;
  }
  return {
    request,
    call: async <T>(path: string, init?: RequestInit) => ((await (await request(path, init)).json()) as { data: T }).data,
  };
}

// dispatchCommand sends an agent-bound request, waits for its result over SSE
// (by the 202's request_id, for timeoutMs else the waitFor default) and
// returns the payload. A refusal (non-2xx) carries the API's message; an
// agent failure throws `failure` (default Error) with the agent's error.
// With refOptional a 2xx without a request_id resolves undefined, else it
// throws. onAccepted hears the request_id before the wait (a caller that
// stops waiting can still recognise the late result). (lib/templates.ts's
// command() is the instance-command variant.)
export async function dispatchCommand(
  waitFor: (ref: string, timeoutMs?: number) => Promise<{ status?: number; error?: string; payload?: unknown }>,
  url: string,
  init: RequestInit,
  opts: {
    timeoutMs?: number;
    failure?: new (message: string) => Error;
    refOptional?: boolean;
    onAccepted?: (ref: string) => void;
  } = {},
): Promise<unknown> {
  const res = await apiFetch(url, init);
  if (!res.ok) throw await readApiError(res);
  const accepted = (await res.json()) as { data?: { request_id?: string } };
  const ref = accepted.data?.request_id;
  if (!ref) {
    if (opts.refOptional) return undefined;
    throw new Error("No request id");
  }
  opts.onAccepted?.(ref);
  const result = await waitFor(ref, opts.timeoutMs);
  if (result.status && result.status !== 0) {
    throw new (opts.failure ?? Error)(result.error || "Operation failed");
  }
  return result.payload;
}
