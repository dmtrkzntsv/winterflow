import type { SharedName } from "@/lib/shared-variables";
import { localId, type AppEditorState } from "@/types/app-config";

// The ${NAME}s an app's compose files use that nothing sets: the compose
// form offers to add them as variables. Only the braced form is read
// ($NAME also appears in shell commands); one with a default (${NAME:-x},
// ${NAME-x}) needs no value, and $${NAME} is compose's escape.

const REF = /(\$*)\$\{([A-Za-z_][A-Za-z0-9_]*)(:?[-?+]?)/g;

// referencedNames are the names compose files interpolate without a
// default, each once, in order.
export function referencedNames(contents: readonly string[]): string[] {
  const out = new Set<string>();
  for (const src of contents) {
    for (const m of src.matchAll(REF)) {
      // An odd number of $ before it: $${X} is a literal ${X}.
      if (m[1].length % 2 === 1) continue;
      const op = m[3];
      if (op.endsWith("-") || op.endsWith("+")) continue;
      out.add(m[2]);
    }
  }
  return [...out];
}

// undeclaredNames are the names the app's compose files use that neither
// its variables nor the shared ones set (WF_* and COMPOSE_PROJECT_NAME
// are the agent's).
export function undeclaredNames(state: AppEditorState, shared: readonly SharedName[]): string[] {
  const set = new Set([...state.config.variables.map((v) => v.name.trim()), ...shared.map((s) => s.name)]);
  const yaml = state.config.files.filter((f) => !f.is_encrypted && /\.ya?ml$/i.test(f.filename.trim()));
  return referencedNames(yaml.map((f) => state.files[f.id] ?? "")).filter(
    (n) => !set.has(n) && !n.startsWith("WF_") && n !== "COMPOSE_PROJECT_NAME",
  );
}

// looksSecret: a name that reads like a password, token or key.
export function looksSecret(name: string): boolean {
  return /PASS|SECRET|TOKEN|KEY/i.test(name);
}

// withVariables adds an empty variable per name (secret when it looks like
// one).
export function withVariables(state: AppEditorState, names: readonly string[]): AppEditorState {
  const rows = names.map((name) => ({ id: localId("var"), name, is_encrypted: looksSecret(name) }));
  return {
    ...state,
    config: { ...state.config, variables: [...state.config.variables, ...rows] },
    variables: { ...state.variables, ...Object.fromEntries(rows.map((r) => [r.id, ""])) },
  };
}
