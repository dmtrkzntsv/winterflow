import { apiBase } from "@/config";

// AuthState is what GET /api/v1/auth/state tells the sign-in pages:
// bootstrap — a fresh (unclaimed) instance, whose first account sets it
// up; registrationEnabled — whether a sign-up would be accepted.
export type AuthState = { bootstrap: boolean; registrationEnabled: boolean };

// AUTH_STATE_TIMEOUT_MS bounds the read: a server that never answers must
// not leave the sign-in pages waiting (the caller's failure path applies).
export const AUTH_STATE_TIMEOUT_MS = 8_000;

// fetchAuthState reads the instance's auth state (no session needed). It
// throws when the server doesn't answer with it, when signal aborts, or
// after AUTH_STATE_TIMEOUT_MS.
export async function fetchAuthState(signal?: AbortSignal): Promise<AuthState> {
  const ctl = new AbortController();
  const abort = () => ctl.abort();
  if (signal?.aborted) ctl.abort();
  else signal?.addEventListener("abort", abort, { once: true });
  const timer = setTimeout(abort, AUTH_STATE_TIMEOUT_MS);
  try {
    const res = await fetch(`${apiBase}/api/v1/auth/state`, { signal: ctl.signal });
    if (!res.ok) throw new Error(`Request failed: ${res.status}`);
    const body = (await res.json()) as { data?: { bootstrap?: unknown; registration_enabled?: unknown } } | null;
    return {
      bootstrap: body?.data?.bootstrap === true,
      registrationEnabled: body?.data?.registration_enabled === true,
    };
  } finally {
    clearTimeout(timer);
    signal?.removeEventListener("abort", abort);
  }
}
