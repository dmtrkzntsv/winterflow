import { useEffect, useState } from "react";

import type { AppDetailPayload } from "@/context/apps-context-base";
import { useApps } from "@/context/use-apps";
import { publishedPorts, type PublishedPort } from "@/lib/host-ports";

// MAX_READS bounds the apps read for their ports when a form opens.
const MAX_READS = 25;

// known holds each app version's published host ports: an app's files
// change only with a new version.
const known = new Map<string, number[]>();

function decode(b64: string): string {
  try {
    const bin = atob(b64);
    return new TextDecoder().decode(Uint8Array.from(bin, (c) => c.charCodeAt(0)));
  } catch {
    return "";
  }
}

// appPublishedPorts are the ports an app's compose files publish (its
// YAML files; a Git app's compose file lives in its repository and is not
// read).
export function appPublishedPorts(detail: AppDetailPayload): PublishedPort[] {
  const out: PublishedPort[] = [];
  for (const f of detail.app?.files ?? []) {
    if (f.encrypted || !/\.ya?ml$/i.test(f.name)) continue;
    out.push(...publishedPorts(decode(f.content)));
  }
  return out;
}

// portsOf are the host ports an app's compose files publish, each once.
export function portsOf(detail: AppDetailPayload): number[] {
  return [...new Set(appPublishedPorts(detail).map((p) => p.port))];
}

// useUsedHostPorts reads the host ports the server's apps publish, one app
// at a time (each read is a command to the agent), remembering each app
// version's. null until read; an app that can't be read is left out.
export function useUsedHostPorts(serverId: string): ReadonlySet<number> | null {
  const { apps, getApp } = useApps();
  const [used, setUsed] = useState<{ serverId: string; ports: ReadonlySet<number> } | null>(null);
  const mine = apps.filter((a) => a.serverId === serverId).slice(0, MAX_READS);
  const versions = mine.map((a) => `${a.id}@${a.version}`).join(",");

  useEffect(() => {
    let live = true;
    void (async () => {
      const ports = new Set<number>();
      for (const v of versions ? versions.split(",") : []) {
        let found = known.get(v);
        if (!found) {
          try {
            found = portsOf(await getApp(v.slice(0, v.lastIndexOf("@"))));
            known.set(v, found);
          } catch {
            found = [];
          }
        }
        if (!live) return;
        for (const p of found) ports.add(p);
      }
      if (live) setUsed({ serverId, ports });
    })();
    return () => {
      live = false;
    };
  }, [serverId, versions, getApp]);

  return used?.serverId === serverId ? used.ports : null;
}

// forgetUsedHostPorts empties the cache (tests).
export function forgetUsedHostPorts() {
  known.clear();
}
