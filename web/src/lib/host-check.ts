import type { HostCheck, Issue } from "@/components/host-hints";
import { apiBaseUrl } from "@/config";
import type { RegisteredDomain } from "@/lib/domains";
import { apiFetch } from "@/lib/api";

// checkHostname asks the API what it thinks of a hostname on this server:
// whether another app claims it, and which registration covers it. null
// when the API refuses (the form validation explains bad input).
export async function checkHostname(
  serverId: string,
  appId: string | undefined,
  host: string,
  signal?: AbortSignal,
): Promise<HostCheck | null> {
  const qs = new URLSearchParams({ server_id: serverId, domain: host });
  if (appId) qs.set("app_id", appId);
  const res = await apiFetch(`${apiBaseUrl}/api/v1/domains/check?${qs}`, {
    credentials: "include",
    signal,
  });
  if (!res.ok) return null;
  const body = (await res.json()) as {
    data?: {
      available?: boolean;
      covered?: boolean;
      registration?: RegisteredDomain | null;
      claims?: { app_name: string }[] | null;
    };
  };
  const data = body.data;
  if (!data) return null;
  const claim = data.claims?.[0];
  const issue: Issue | undefined =
    data.available === false && claim
      ? { kind: "taken", app: claim.app_name }
      : data.covered === false
        ? { kind: "uncovered" }
        : undefined;
  return { issue, registration: data.registration ?? undefined };
}
