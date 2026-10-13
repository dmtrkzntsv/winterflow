// readApiError turns a failed API response into an Error carrying the
// server's explanation (the envelope's `message`), falling back to the
// status when the body has none or isn't JSON.
export async function readApiError(res: Response): Promise<Error> {
  const body = (await res.json().catch(() => null)) as { message?: unknown } | null;
  const message = typeof body?.message === "string" ? body.message.trim() : "";
  return new Error(message || `Request failed: ${res.status}`);
}

// dispatchCommand sends an agent-bound request, waits for its result over SSE
// (by the 202's request_id, for timeoutMs else the waitFor default) and
// returns the payload. A refusal (non-2xx) carries the API's message; an
// agent failure throws `failure` (default Error) with the agent's error.
// With refOptional a 2xx without a request_id resolves undefined, else it
// throws. (lib/templates.ts's command() is the instance-command variant.)
export async function dispatchCommand(
  waitFor: (ref: string, timeoutMs?: number) => Promise<{ status?: number; error?: string; payload?: unknown }>,
  url: string,
  init: RequestInit,
  opts: { timeoutMs?: number; failure?: new (message: string) => Error; refOptional?: boolean } = {},
): Promise<unknown> {
  const res = await fetch(url, { credentials: "include", ...init });
  if (!res.ok) throw await readApiError(res);
  const accepted = (await res.json()) as { data?: { request_id?: string } };
  const ref = accepted.data?.request_id;
  if (!ref) {
    if (opts.refOptional) return undefined;
    throw new Error("No request id");
  }
  const result = await waitFor(ref, opts.timeoutMs);
  if (result.status && result.status !== 0) {
    throw new (opts.failure ?? Error)(result.error || "Operation failed");
  }
  return result.payload;
}
