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

// entryHostPorts are the host ports one short-syntax entry publishes.
export function entryHostPorts(raw: string): number[] {
  let s = unquote(raw);
  if (s === "" || s.includes("$") || s.includes("{")) return [];
  s = s.replace(/\/[a-z]+$/i, "");
  // A bracketed IPv6 host address goes first.
  if (s.startsWith("[")) {
    const end = s.indexOf("]:");
    if (end < 0) return [];
    s = s.slice(end + 2);
    const parts = s.split(":");
    return parts.length === 2 ? portRange(parts[0]) : [];
  }
  const parts = s.split(":");
  if (parts.length < 2) return [];
  // HOST:CONTAINER, IP:HOST:CONTAINER (the host part second to last).
  return portRange(parts[parts.length - 2]);
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

// publishedHostPorts are the host ports a compose file's services publish
// (each once, in order).
export function publishedHostPorts(content: string): number[] {
  const found = new Set<number>();
  const lines = content.split(/\r?\n/);
  for (let i = 0; i < lines.length; i++) {
    const line = stripComment(lines[i]);
    const m = /^(\s*)ports\s*:\s*(.*)$/.exec(line);
    if (!m) continue;
    const keyIndent = m[1].length;
    const rest = m[2].trim();
    if (rest.startsWith("[")) {
      for (const item of rest.replace(/^\[|\]$/g, "").split(",")) for (const p of entryHostPorts(item)) found.add(p);
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
      for (const p of entryHostPorts(dash[1])) found.add(p);
      i = j;
    }
  }
  return [...found];
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
