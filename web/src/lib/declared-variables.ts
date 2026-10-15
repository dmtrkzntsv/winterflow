// The x-winterflow variables form's rules, mirroring the agent's
// (internal/domain/compose/xwinterflow CheckValues): a required variable
// needs a value, a plain value can't hold $ and must match the pattern. The
// API and the agent are the boundary; this is feedback as you type.
import { ENCRYPTED } from "@/lib/ecies";
import { localId, type AppEditorState, type AppVariableMeta, type DeclaredVariable } from "@/types/app-config";

// RE2_ONLY matches syntax RE2 reads differently from JavaScript (or that
// JavaScript rejects): \p/\P classes, POSIX [:classes:], \A, \z, \Q..\E,
// \x{...} and (?flags) / (?P<name>) groups. A pattern holding any is left to
// the server: the browser check must never reject what RE2 accepts.
const RE2_ONLY = /\\[pPAzQ]|\\x\{|\[:|\(\?/;

// matchesPattern tests value against an RE2 pattern as the agent does (the
// whole value); a pattern JavaScript can't compile, or reads differently, is
// left to the server.
export function matchesPattern(pattern: string | undefined, value: string): boolean {
  if (!pattern || RE2_ONLY.test(pattern)) return true;
  try {
    return new RegExp(`^(?:${pattern})$`).test(value);
  } catch {
    return true;
  }
}

// declaredRow is the variable row holding v's value (by name).
export function declaredRow(state: AppEditorState, v: DeclaredVariable): AppVariableMeta | undefined {
  return state.config.variables.find((row) => row.name.trim() === v.name);
}

// declaredValueError is what's wrong with value for v ("" when nothing):
// held says a stored secret stands for it (left as is or empty).
export function declaredValueError(v: DeclaredVariable, value: string, held: boolean): string {
  if (held && (value === "" || value === ENCRYPTED)) return "";
  if (value === "") return v.required ? "Enter a value: this variable is required." : "";
  if (!v.encrypted && value.includes("$")) return "A plain value can't contain $.";
  if (!matchesPattern(v.pattern, value)) return `Must match ${v.pattern}.`;
  return "";
}

// setDeclaredValue sets v's value, adding its row (secret when v is) the
// first time; a row of a secret variable becomes secret.
export function setDeclaredValue(state: AppEditorState, v: DeclaredVariable, value: string): AppEditorState {
  const row = declaredRow(state, v);
  if (row) {
    const variables = v.encrypted && !row.is_encrypted
      ? state.config.variables.map((r) => (r.id === row.id ? { ...r, is_encrypted: true } : r))
      : state.config.variables;
    return { ...state, config: { ...state.config, variables }, variables: { ...state.variables, [row.id]: value } };
  }
  const id = localId("var");
  return {
    ...state,
    config: { ...state.config, variables: [...state.config.variables, { id, name: v.name, is_encrypted: v.encrypted }] },
    variables: { ...state.variables, [id]: value },
  };
}
