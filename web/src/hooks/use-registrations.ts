import { useCallback, useEffect, useState } from "react";

import { apiBaseUrl } from "@/config";
import type { RegisteredDomain } from "@/lib/domains";

// useRegistrations reads a server's registered domains from the API's index
// (a plain GET: no agent round-trip). They only drive the editor's
// suggestions, so a failure just leaves the list empty. refresh re-reads it.
export function useRegistrations(serverId: string) {
  const [registrations, setRegistrations] = useState<RegisteredDomain[]>([]);
  const [reloads, setReloads] = useState(0);

  useEffect(() => {
    const ctrl = new AbortController();
    const qs = new URLSearchParams({ server_id: serverId });
    fetch(`${apiBaseUrl}/api/v1/domain/get-domains?${qs}`, {
      credentials: "include",
      signal: ctrl.signal,
    })
      .then((res) => (res.ok ? res.json() : null))
      .then((body: { data?: { domains?: RegisteredDomain[] | null } } | null) => {
        if (body) setRegistrations(body.data?.domains ?? []);
      })
      .catch(() => {
        // Advisory (or aborted): keep what we have.
      });
    return () => ctrl.abort();
  }, [serverId, reloads]);

  const refresh = useCallback(() => setReloads((n) => n + 1), []);
  return { registrations, refresh };
}
