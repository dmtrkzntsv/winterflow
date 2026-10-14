import type { Volume } from "@/context/use-docker";
import type { StatusTone } from "@/lib/status";

const VOLUME_NAME_RE = /^[a-zA-Z0-9][a-zA-Z0-9_.-]*$/;

// volumeNameError mirrors the agent's ValidateVolumeName so the dialog can say
// what's wrong before a round-trip; "" means valid.
export function volumeNameError(name: string): string {
  if (name.length > 255) return "Use at most 255 characters.";
  if (!VOLUME_NAME_RE.test(name)) {
    return "Start with a letter or digit; use only letters, digits, dots, dashes and underscores.";
  }
  return "";
}

// shortVolumeName drops the compose project prefix ("wf-<id>_data" → "data"):
// the App column already names the owner, so the key is what tells rows
// apart. Callers keep the full name in a title and in confirmations.
export function shortVolumeName(v: Volume): string {
  // Anonymous volumes are 64-hex hashes; 12 chars is Docker's own short id.
  if (v.anonymous && /^[0-9a-f]{64}$/.test(v.name)) {
    return `${v.name.slice(0, 12)}…`;
  }
  const prefix = v.app_id && v.project ? `${v.project}_` : "";
  return prefix && v.name.startsWith(prefix)
    ? v.name.slice(prefix.length)
    : v.name;
}

export type VolumeGroup = { label: string; volumes: Volume[] };

// groupVolumes splits the (backend name-sorted) list so Winterflow's volumes
// don't drown in whatever else runs on the host. Order within a group is
// preserved; empty groups are dropped.
export function groupVolumes(volumes: Volume[]): VolumeGroup[] {
  const groups: VolumeGroup[] = [
    { label: "Winterflow apps", volumes: volumes.filter((v) => v.app_id) },
    {
      label: "Other volumes",
      volumes: volumes.filter((v) => !v.app_id && !v.anonymous),
    },
    {
      label: "Anonymous",
      volumes: volumes.filter((v) => !v.app_id && v.anonymous),
    },
  ];
  return groups.filter((g) => g.volumes.length > 0);
}

// volumeState says whether a volume is used, in plain words: mounted by a
// container, labelled for a Winterflow app that is no longer here, or
// neither.
export function volumeState(v: Volume): { tone: StatusTone; label: string } {
  if (v.in_use) return { tone: "ok", label: "In use" };
  if (v.orphan) return { tone: "warn", label: "Not used by any app" };
  return { tone: "idle", label: "Unused" };
}
