import { formatBytes } from "@/lib/utils";

export type ServerFact = { label: string; value: string };

// serverFacts lists what a server's agent reported about its machine, in
// the order Settings → Server → General shows them; an unreported fact is
// left out (never shown as 0).
export function serverFacts(caps: Record<string, string>): ServerFact[] {
  const facts: ServerFact[] = [];
  if (caps.server_ip) facts.push({ label: "IP address", value: caps.server_ip });
  const cores = Number(caps.system_cpu_cores);
  if (Number.isFinite(cores) && cores > 0) {
    facts.push({ label: "CPU", value: cores === 1 ? "1 core" : `${cores} cores` });
  }
  const memory = formatBytes(caps.system_memory_total);
  if (memory) facts.push({ label: "Memory", value: memory });
  const disk = formatBytes(caps.system_disk_total);
  if (disk) facts.push({ label: "Disk", value: disk });
  return facts;
}

// versionLabel writes an agent version the way the UI shows it, "v1.2.3",
// whether the agent reported "1.2.3" or "v1.2.3"; "" stays "".
export function versionLabel(version: string): string {
  const v = version.trim();
  if (!v) return "";
  return /^v/i.test(v) ? v : `v${v}`;
}
