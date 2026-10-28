import { useCallback, useEffect, useState } from "react";

import { apiBaseUrl } from "@/config";
import { useServers } from "@/context/use-servers";
import type { RegisteredDomain } from "@/lib/domains";
import { apiFetch } from "@/lib/api";

const NONE: RegisteredDomain[] = [];

// useRegistrations reads a server's registered domains from the API's index
// (a plain GET: no agent round-trip). They are advisory (the editor's
// suggestions, an address's HTTPS state), so a failure just leaves the list
// empty. refresh re-reads it. enabled false (a server without the built-in
// proxy) asks nothing and lists none; another server's list is never shown.
export function useRegistrations(serverId: string, enabled = true) {
  const [loaded, setLoaded] = useState<{ serverId: string; list: RegisteredDomain[] } | null>(null);
  const [reloads, setReloads] = useState(0);

  useEffect(() => {
    if (!enabled) return;
    const ctrl = new AbortController();
    const qs = new URLSearchParams({ server_id: serverId });
    apiFetch(`${apiBaseUrl}/api/v1/domain/get-domains?${qs}`, {
      credentials: "include",
      signal: ctrl.signal,
    })
      .then((res) => (res.ok ? res.json() : null))
      .then((body: { data?: { domains?: RegisteredDomain[] | null } } | null) => {
        if (body) setLoaded({ serverId, list: body.data?.domains ?? [] });
      })
      .catch(() => {
        // Advisory (or aborted): keep what we have.
      });
    return () => ctrl.abort();
  }, [serverId, reloads, enabled]);

  const refresh = useCallback(() => setReloads((n) => n + 1), []);
  const registrations = enabled && loaded?.serverId === serverId ? loaded.list : NONE;
  return { registrations, refresh };
}

// useAddressRegistrations is what an app address's HTTPS state reads: the
// server's registered domains, asked for only when it runs the built-in
// proxy (else none).
export function useAddressRegistrations(serverId: string | null | undefined): RegisteredDomain[] {
  const { servers } = useServers();
  const ingress = Boolean(serverId && servers.find((s) => s.id === serverId)?.features?.ingress);
  return useRegistrations(serverId ?? "", ingress).registrations;
}
