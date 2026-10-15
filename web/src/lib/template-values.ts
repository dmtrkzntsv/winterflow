// An instance's values in the browser: the form's checks over the
// variables a template's compose files declare (x-winterflow), mirroring
// the API's (internal/domain/template Resolve, xwinterflow CheckValues).
// A required variable needs a value and a plain value can't hold $: those
// block a save. A pattern mismatch is only a hint (lib/declared-variables):
// the API (a plain value) and the agent (a secret) decide.
import { declaredValueError } from "@/lib/declared-variables";
import type { InstanceView } from "@/lib/templates";
import type { DeclaredVariable } from "@/types/app-config";

// variableErrors are a values form's blocking problems by name. kept are
// the secrets the agent holds: a blank field keeps them.
export function variableErrors(
  vars: DeclaredVariable[] | null | undefined,
  values: Record<string, string>,
  kept: readonly string[] = [],
): Record<string, string> {
  const out: Record<string, string> = {};
  for (const v of vars ?? []) {
    const err = declaredValueError(v, values[v.name] ?? "", v.encrypted && kept.includes(v.name));
    if (err) out[v.name] = err;
  }
  return out;
}

// splitValues splits a values form by name: plain values and the typed
// secrets (to seal), each "" when blank.
export function splitValues(
  vars: DeclaredVariable[] | null | undefined,
  values: Record<string, string>,
): { plain: Record<string, string>; typed: Record<string, string> } {
  const plain: Record<string, string> = {};
  const typed: Record<string, string> = {};
  for (const v of vars ?? []) (v.encrypted ? typed : plain)[v.name] = values[v.name] ?? "";
  return { plain, typed };
}

// requiredFirst orders a form: required variables first, each group in the
// compose file's order.
export function requiredFirst(vars: DeclaredVariable[] | null | undefined): DeclaredVariable[] {
  const list = vars ?? [];
  return [...list.filter((v) => v.required), ...list.filter((v) => !v.required)];
}

// upgradeNeeds are the variables of the target revision that need a value
// the instance doesn't hold: a required plain one without a stored value,
// a required secret it was never given. A pending instance holds no secret
// at all: every required secret is asked for.
export function upgradeNeeds(target: DeclaredVariable[] | null | undefined, inst: InstanceView): DeclaredVariable[] {
  const pending = inst.state === "pending";
  return (target ?? []).filter((v) => {
    if (!v.required) return false;
    if (v.encrypted) return pending || !(v.name in (inst.secrets ?? {}));
    return !inst.values?.[v.name];
  });
}

export type DefaultChange = { name: string; from: string; to: string };

// defaultChanges are the plain variables an upgrade changes without anyone
// typing a value: ones the instance holds no value for whose compose file
// value differs between the revisions (none reads as ""). A variable new in
// the target is not a change.
export function defaultChanges(
  from: DeclaredVariable[] | null | undefined,
  to: DeclaredVariable[] | null | undefined,
  inst: Pick<InstanceView, "values">,
): DefaultChange[] {
  const before = new Map((from ?? []).filter((v) => !v.encrypted).map((v) => [v.name, v.default ?? ""]));
  const held = inst.values ?? {};
  const out: DefaultChange[] = [];
  for (const v of to ?? []) {
    const was = before.get(v.name);
    if (v.encrypted || was === undefined || held[v.name]) continue;
    const now = v.default ?? "";
    if (now !== was) out.push({ name: v.name, from: was, to: now });
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
