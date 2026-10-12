import { useEffect, useMemo, useState } from "react";

import { Badge } from "@/components/ui/badge";
import {
  createSharedVariablesApi,
  sharedNames,
  type SharedName,
  type SharedVariablesApi,
} from "@/lib/shared-variables";

const SCOPE_LABEL: Record<SharedName["scope"], string> = { org: "org", server: "server", builtin: "built-in" };

// useSharedNames loads the shared names apps on serverId can reference
// (names and scopes only); empty while loading or when the listing fails.
// eslint-disable-next-line react-refresh/only-export-components -- hook and its component belong together (brief/test contract)
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

// SharedVariablesHint is the editor's collapsed, read-only list of shared
// variables an app can use as ${NAME} — never a value.
export function SharedVariablesHint({ shared }: { shared: SharedName[] }) {
  if (shared.length === 0) return null;
  return (
    <details className="rounded-md border p-3 text-sm">
      <summary className="cursor-pointer text-muted-foreground">
        Shared variables available to this app ({shared.length})
      </summary>
      <ul className="mt-2 grid gap-1 sm:grid-cols-2">
        {shared.map((s) => (
          <li key={`${s.scope}:${s.name}`} className="flex items-center gap-2">
            <code className="text-xs">{s.name}</code>
            <Badge variant="outline">{SCOPE_LABEL[s.scope]}</Badge>
          </li>
        ))}
      </ul>
    </details>
  );
}
