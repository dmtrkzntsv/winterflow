// readApiError turns a failed API response into an Error carrying the
// server's explanation (the envelope's `message`), falling back to the
// status when the body has none or isn't JSON.
export async function readApiError(res: Response): Promise<Error> {
  const body = (await res.json().catch(() => null)) as { message?: unknown } | null;
  const message = typeof body?.message === "string" ? body.message.trim() : "";
  return new Error(message || `Request failed: ${res.status}`);
}
