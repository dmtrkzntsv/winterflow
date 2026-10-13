// Parameter values in the browser: instant feedback while a value is typed
// (the API checks everything again with Go's RE2), the preview of what a
// value renders into (app name, hosts) and which values an upgrade needs.
import type { InstanceView, Parameter, TemplateBody } from "@/lib/templates";

// RE2_ONLY matches constructs JavaScript reads differently or not at all:
// POSIX classes, \p classes, \z \A \Q \E \C, (?P<name>), ungreedy and
// negated flags, \x{hex} and \S (ASCII in RE2, Unicode in JS: a
// non-breaking space). A pattern using one is left to the API.
const RE2_ONLY = /\[\[:|\\[pPzAQEC]|\(\?P<|\(\?[imsU]*(-|U)|\\x\{|\\S/;

// compiled holds compilePattern's results by pattern (forms check every
// value on each keystroke), at most MAX_COMPILED of them.
const MAX_COMPILED = 256;
const compiled = new Map<string, RegExp | null>();

// compilePattern turns a parameter's RE2 pattern into a JS RegExp, or null
// when JavaScript can't be trusted to agree (then only the API checks it).
// A leading (?i), (?m) or (?s) becomes the matching JS flag; the u flag
// makes JS count code points as RE2 does (and refuse escapes it doesn't
// know: those patterns are the API's). A known limit: JS's . also stops
// at \r (and U+2028/9), RE2's only at \n.
export function compilePattern(pattern: string | undefined): RegExp | null {
  if (!pattern) return null;
  let re = compiled.get(pattern);
  if (re === undefined) {
    // Shared safely: without g or y a RegExp keeps no state between tests.
    if (compiled.size >= MAX_COMPILED) compiled.clear();
    re = compile(pattern);
    compiled.set(pattern, re);
  }
  return re;
}

function compile(pattern: string): RegExp | null {
  if (RE2_ONLY.test(pattern)) return null;
  let src = pattern;
  let flags = "";
  const lead = /^\(\?([ims]+)\)/.exec(src);
  if (lead) {
    flags = [...new Set(lead[1])].join("");
    src = src.slice(lead[0].length);
  }
  try {
    return new RegExp(src, flags + "u");
  } catch {
    return null;
  }
}

// valueError checks a plain value as typed ("" = not given: the default
// applies). "" means fine; the API has the last word.
export function valueError(p: Parameter, value: string): string {
  if (value.includes("$")) return "May not contain $";
  if (value === "") return p.required && !p.default ? "Required" : "";
  const re = compilePattern(p.pattern);
  if (re && !re.test(value)) return `Must match ${p.pattern}`;
  return "";
}

// fieldErrors are a parameter form's problems by name: each plain value as
// valueError has it, a required secret neither typed nor kept (kept: the
// secrets the agent already holds, which a blank field leaves as they are).
export function fieldErrors(
  params: Parameter[] | null | undefined,
  values: Record<string, string>,
  kept: readonly string[] = [],
): Record<string, string> {
  const out: Record<string, string> = {};
  for (const p of params ?? []) {
    const v = values[p.name] ?? "";
    const err = p.secret ? (p.required && v === "" && !kept.includes(p.name) ? "Required" : "") : valueError(p, v);
    if (err) out[p.name] = err;
  }
  return out;
}

// splitValues splits a parameter form's values by name: plain values and
// the typed secrets (to seal), each "" when blank.
export function splitValues(
  params: Parameter[] | null | undefined,
  values: Record<string, string>,
): { plain: Record<string, string>; typed: Record<string, string> } {
  const plain: Record<string, string> = {};
  const typed: Record<string, string> = {};
  for (const p of params ?? []) (p.secret ? typed : plain)[p.name] = values[p.name] ?? "";
  return { plain, typed };
}

// requiredFirst orders a form: required parameters first, each group in the
// template's order.
export function requiredFirst(params: Parameter[] | null | undefined): Parameter[] {
  const list = params ?? [];
  return [...list.filter((p) => p.required), ...list.filter((p) => !p.required)];
}

// plainValues are the values a render uses: each plain parameter's given
// value, else its default, else "".
export function plainValues(params: Parameter[] | null | undefined, given: Record<string, string>): Record<string, string> {
  const out: Record<string, string> = {};
  for (const p of params ?? []) {
    if (p.secret) continue;
    out[p.name] = given[p.name] || p.default || "";
  }
  return out;
}

// REF matches a ${NAME} reference (global: use it with replace or matchAll).
export const REF = /\$\{([^}]*)\}/g;

// expand substitutes ${NAME} with plain values, as the API renders the app
// name, ingress hosts and ports.
export function expand(s: string, plain: Record<string, string>): string {
  return s.replace(REF, (_, name: string) => plain[name] ?? "");
}

// missingRefs are the parameters s references that have no value yet.
export function missingRefs(s: string, plain: Record<string, string>): string[] {
  return [...s.matchAll(REF)].map((m) => m[1]).filter((name) => !plain[name]);
}

export type HostPreview = { host: string; port: string; missing: string[] };

// previewHosts renders the body's ingress rows with the values so far.
export function previewHosts(body: TemplateBody, plain: Record<string, string>): HostPreview[] {
  return (body.ingress ?? []).map((row) => ({
    host: expand(row.host, plain),
    port: expand(String(row.port), plain),
    missing: [...missingRefs(row.host, plain), ...missingRefs(String(row.port), plain)],
  }));
}

// upgradeNeeds are the parameters of the target revision that need a value
// the instance doesn't hold: a required plain parameter without a default
// or a stored value, and a required secret it was never given. A pending
// instance holds no secret at all: every required secret is asked for.
export function upgradeNeeds(target: Parameter[] | null | undefined, inst: InstanceView): Parameter[] {
  const pending = inst.state === "pending";
  return (target ?? []).filter((p) => {
    if (!p.required) return false;
    if (p.secret) return pending || !(p.name in (inst.secrets ?? {}));
    return !p.default && !inst.parameters?.[p.name];
  });
}

export type DefaultChange = { name: string; from: string; to: string };

// defaultChanges are the plain parameters an upgrade from one revision's
// parameters to another's changes without anyone typing a value: ones the
// instance holds no value for (a stored "" is a value) whose default differs
// (no default renders as ""). A parameter new in the target is not a change.
export function defaultChanges(
  from: Parameter[] | null | undefined,
  to: Parameter[] | null | undefined,
  inst: Pick<InstanceView, "parameters">,
): DefaultChange[] {
  const before = new Map((from ?? []).filter((p) => !p.secret).map((p) => [p.name, p.default ?? ""]));
  const held = inst.parameters ?? {};
  const out: DefaultChange[] = [];
  for (const p of to ?? []) {
    const was = before.get(p.name);
    if (p.secret || was === undefined || p.name in held) continue;
    const now = p.default ?? "";
    if (now !== was) out.push({ name: p.name, from: was, to: now });
  }
  return out;
}

// newKey is the key the browser gives an instance it creates (a UUID v4):
// the API replays a create with the same key instead of making a second
// app. crypto.randomUUID exists only on secure contexts, and self-hosted
// installs are often reached over plain http, so it is built from
// getRandomValues (available everywhere).
export function newKey(): string {
  const b = crypto.getRandomValues(new Uint8Array(16));
  b[6] = (b[6] & 0x0f) | 0x40;
  b[8] = (b[8] & 0x3f) | 0x80;
  const h = Array.from(b, (x) => x.toString(16).padStart(2, "0")).join("");
  return `${h.slice(0, 8)}-${h.slice(8, 12)}-${h.slice(12, 16)}-${h.slice(16, 20)}-${h.slice(20)}`;
}
