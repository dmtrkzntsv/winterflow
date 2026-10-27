import { apiBase } from "@/config";
import { apiFetch } from "@/lib/api";

// SignInProvider is a configured external sign-in (Google, Apple, an OIDC
// provider): id is its route segment, name what the button says.
export type SignInProvider = { id: string; name: string };

// AuthState is what GET /api/v1/auth/state tells the sign-in pages:
// bootstrap — a fresh (unclaimed) instance, whose first account sets it
// up; registrationEnabled — whether a sign-up would be accepted;
// providers — the external sign-ins; oauthServer — whether MCP clients can
// be connected (the Connected apps page).
export type AuthState = {
  bootstrap: boolean;
  registrationEnabled: boolean;
  providers: SignInProvider[];
  oauthServer: boolean;
};

// AUTH_STATE_TIMEOUT_MS bounds the read: a server that never answers must
// not leave the sign-in pages waiting (the caller's failure path applies).
export const AUTH_STATE_TIMEOUT_MS = 8_000;

function parseProviders(v: unknown): SignInProvider[] {
  if (!Array.isArray(v)) return [];
  return v.flatMap((p: unknown) => {
    const { id, name } = (p ?? {}) as { id?: unknown; name?: unknown };
    return typeof id === "string" && id !== "" ? [{ id, name: typeof name === "string" && name ? name : id }] : [];
  });
}

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
    const res = await apiFetch(`${apiBase}/api/v1/auth/state`, { signal: ctl.signal });
    if (!res.ok) throw new Error(`Request failed: ${res.status}`);
    const body = (await res.json()) as {
      data?: { bootstrap?: unknown; registration_enabled?: unknown; providers?: unknown; oauth_server?: unknown };
    } | null;
    return {
      bootstrap: body?.data?.bootstrap === true,
      registrationEnabled: body?.data?.registration_enabled === true,
      providers: parseProviders(body?.data?.providers),
      oauthServer: body?.data?.oauth_server === true,
    };
  } finally {
    clearTimeout(timer);
    signal?.removeEventListener("abort", abort);
  }
}

let cached: Promise<AuthState> | null = null;

// loadAuthState is fetchAuthState read once per page load, for the signed-in
// pages (which providers can be linked, whether apps can be connected); a
// failed read is forgotten so the next call asks again.
export function loadAuthState(): Promise<AuthState> {
  if (!cached) {
    cached = fetchAuthState().catch((e: unknown) => {
      cached = null;
      throw e;
    });
  }
  return cached;
}

// resetAuthStateCache forgets loadAuthState's answer (tests).
export function resetAuthStateCache() {
  cached = null;
}
