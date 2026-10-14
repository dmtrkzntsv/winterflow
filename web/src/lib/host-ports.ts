// Host ports a compose file publishes, read from the ports: short syntax
// ("8080:80", "127.0.0.1:8080:80", "8080-8081:80-81/tcp") without a YAML
// parser: the new-app form only needs them to pick a port nobody uses.
// Interpolated ports ("${PORT}:80") and container-only entries ("80") name
// no host port and are left out, as is the long syntax.

// The range the compose form prefills a free host port from.
export const HOST_PORT_FIRST = 8080;
export const HOST_PORT_LAST = 8999;

const unquote = (s: string) => s.trim().replace(/^(["'])(.*)\1$/, "$2").trim();

// stripComment drops a trailing "# ..." outside quotes.
function stripComment(line: string): string {
  let quote = "";
  for (let i = 0; i < line.length; i++) {
    const c = line[i];
    if (quote) {
      if (c === quote) quote = "";
    } else if (c === '"' || c === "'") {
      quote = c;
    } else if (c === "#" && (i === 0 || /\s/.test(line[i - 1]))) {
      return line.slice(0, i);
    }
  }
  return line;
}

// EntryBinding is what one short-syntax entry publishes: the host address
// it binds ("" = every interface) and its host ports.
export type EntryBinding = { host: string; ports: number[] };

// entryBinding reads one short-syntax entry.
export function entryBinding(raw: string): EntryBinding {
  const none = { host: "", ports: [] };
  let s = unquote(raw);
  if (s === "" || s.includes("$") || s.includes("{")) return none;
  s = s.replace(/\/[a-z]+$/i, "");
  // A bracketed IPv6 host address goes first.
  if (s.startsWith("[")) {
    const end = s.indexOf("]:");
    if (end < 0) return none;
    const host = s.slice(1, end);
    const parts = s.slice(end + 2).split(":");
    return parts.length === 2 ? { host, ports: portRange(parts[0]) } : none;
  }
  const parts = s.split(":");
  if (parts.length < 2) return none;
  // HOST:CONTAINER, IP:HOST:CONTAINER (the host part second to last).
  return { host: parts.length > 2 ? parts.slice(0, -2).join(":") : "", ports: portRange(parts[parts.length - 2]) };
}

// entryHostPorts are the host ports one short-syntax entry publishes.
export function entryHostPorts(raw: string): number[] {
  return entryBinding(raw).ports;
}

function portRange(spec: string): number[] {
  const m = /^(\d+)(?:-(\d+))?$/.exec(spec.trim());
  if (!m) return [];
  const from = Number(m[1]);
  const to = m[2] ? Number(m[2]) : from;
  if (from < 1 || to > 65535 || to < from || to - from > 1000) return [];
  const out: number[] = [];
  for (let p = from; p <= to; p++) out.push(p);
  return out;
}

const indentOf = (line: string) => line.length - line.trimStart().length;

// PublishedPort is a host port a compose service publishes, with the host
// address it binds ("" = every interface).
export type PublishedPort = { service: string; host: string; port: number };

// serviceAt is the key that owns the line at index i, indented below
// indent: the service of a ports key ("" when there is none).
function serviceAt(lines: string[], i: number, indent: number): string {
  for (let j = i - 1; j >= 0; j--) {
    const line = stripComment(lines[j]);
    if (line.trim() === "" || indentOf(line) >= indent) continue;
    const key = /^\s*["']?([^"'\s:#]+)["']?\s*:/.exec(line);
    return key ? key[1] : "";
  }
  return "";
}

// publishedPorts are the host ports a compose file's services publish, in
// order, with their service and bind address.
export function publishedPorts(content: string): PublishedPort[] {
  const out: PublishedPort[] = [];
  const lines = content.split(/\r?\n/);
  const add = (service: string, entry: string) => {
    const { host, ports } = entryBinding(entry);
    for (const port of ports) out.push({ service, host, port });
  };
  for (let i = 0; i < lines.length; i++) {
    const line = stripComment(lines[i]);
    const m = /^(\s*)ports\s*:\s*(.*)$/.exec(line);
    if (!m) continue;
    const keyIndent = m[1].length;
    const service = serviceAt(lines, i, keyIndent);
    const rest = m[2].trim();
    if (rest.startsWith("[")) {
      for (const item of rest.replace(/^\[|\]$/g, "").split(",")) add(service, item);
      continue;
    }
    // A block sequence: "- entry" lines, indented past the key (or level
    // with it, which YAML allows).
    for (let j = i + 1; j < lines.length; j++) {
      const item = stripComment(lines[j]);
      if (item.trim() === "") continue;
      const indent = indentOf(item);
      const dash = /^\s*-\s+(.*)$/.exec(item);
      if (indent < keyIndent || (indent === keyIndent && !dash)) break;
      if (!dash) continue; // the long syntax's keys
      add(service, dash[1]);
      i = j;
    }
  }
  return out;
}

// publishedHostPorts are the host ports a compose file's services publish
// (each once, in order).
export function publishedHostPorts(content: string): number[] {
  return [...new Set(publishedPorts(content).map((p) => p.port))];
}

// isLoopback: a bind address only the server itself reaches.
export function isLoopback(host: string): boolean {
  const h = host.toLowerCase();
  return h === "localhost" || h === "::1" || /^127\./.test(h);
}

// firstFreeHostPort is the first port of the prefill range not in used
// (undefined when all are).
export function firstFreeHostPort(used: ReadonlySet<number>): number | undefined {
  for (let p = HOST_PORT_FIRST; p <= HOST_PORT_LAST; p++) if (!used.has(p)) return p;
  return undefined;
}

// randomHostPort is a port of the prefill range, for when the ports in use
// can't be read.
export function randomHostPort(random: () => number = Math.random): number {
  return HOST_PORT_FIRST + Math.floor(random() * (HOST_PORT_LAST - HOST_PORT_FIRST + 1));
}
