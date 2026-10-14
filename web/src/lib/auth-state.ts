import { apiBase } from "@/config";

// AuthState is what GET /api/v1/auth/state tells the sign-in pages:
// bootstrap — a fresh (unclaimed) instance, whose first account sets it
// up; registrationEnabled — whether a sign-up would be accepted.
export type AuthState = { bootstrap: boolean; registrationEnabled: boolean };

// fetchAuthState reads the instance's auth state (no session needed). It
// throws when the server doesn't answer with it.
export async function fetchAuthState(signal?: AbortSignal): Promise<AuthState> {
  const res = await fetch(`${apiBase}/api/v1/auth/state`, { signal });
  if (!res.ok) throw new Error(`Request failed: ${res.status}`);
  const body = (await res.json()) as { data?: { bootstrap?: unknown; registration_enabled?: unknown } } | null;
  return {
    bootstrap: body?.data?.bootstrap === true,
    registrationEnabled: body?.data?.registration_enabled === true,
  };
}
