import type { Network } from "@/context/use-docker";

// Docker's own networks: always there, never removable.
export const BUILTIN_NETWORKS = new Set(["bridge", "host", "none"]);

// ListedNetwork is a network with what the list shows for it: its name
// without the app's compose project prefix ("default" for
// "wf-<id>_default").
export type ListedNetwork = { network: Network; short: string };

// NetworkGroup is one app's networks (appId set, labelled by its name), or
// the other networks on the host.
export type NetworkGroup = { label: string; appId?: string; networks: ListedNetwork[] };

// groupNetworks sorts the server's networks the way the Volumes list
// sorts volumes: each app's networks under the app's name (a compose
// project wf-<app id> names them wf-<app id>_<network>), then "Other",
// with Docker's built-in ones apart (builtin). Order within a group is the
// list's; empty groups are left out.
export function groupNetworks(
  networks: readonly Network[],
  apps: readonly { id: string; name: string }[],
): { groups: NetworkGroup[]; builtin: Network[] } {
  const byApp = new Map<string, ListedNetwork[]>();
  const other: ListedNetwork[] = [];
  const builtin: Network[] = [];
  for (const n of networks) {
    if (BUILTIN_NETWORKS.has(n.name)) {
      builtin.push(n);
      continue;
    }
    const app = apps.find((a) => n.name.startsWith(`wf-${a.id}_`));
    if (!app) {
      other.push({ network: n, short: n.name });
      continue;
    }
    const list = byApp.get(app.id) ?? [];
    list.push({ network: n, short: n.name.slice(`wf-${app.id}_`.length) });
    byApp.set(app.id, list);
  }
  const groups: NetworkGroup[] = apps
    .filter((a) => byApp.has(a.id))
    .map((a) => ({ label: a.name.trim() || "Unnamed app", appId: a.id, networks: byApp.get(a.id)! }))
    .sort((a, b) => a.label.localeCompare(b.label));
  if (other.length > 0) groups.push({ label: "Other", networks: other });
  return { groups, builtin };
}
