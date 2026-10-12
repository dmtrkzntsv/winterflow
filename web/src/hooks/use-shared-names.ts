import { useEffect, useMemo, useState } from "react";

import {
  createSharedVariablesApi,
  sharedNames,
  type SharedName,
  type SharedVariablesApi,
} from "@/lib/shared-variables";

// useSharedNames loads the shared names apps on serverId can reference
// (names and scopes only); empty while loading or when the listing fails.
export function useSharedNames(serverId?: string, api?: SharedVariablesApi): SharedName[] {
  const client = useMemo(() => api ?? createSharedVariablesApi(), [api]);
  const [loaded, setLoaded] = useState<{ serverId: string; names: SharedName[] } | null>(null);
  useEffect(() => {
    if (!serverId) return;
    let live = true;
    client.listServer(serverId).then(
      (v) => {
        if (live) setLoaded({ serverId, names: sharedNames(v) });
      },
      () => {
        if (live) setLoaded({ serverId, names: [] });
      },
    );
    return () => {
      live = false;
    };
  }, [client, serverId]);
  return loaded && loaded.serverId === serverId ? loaded.names : [];
}
