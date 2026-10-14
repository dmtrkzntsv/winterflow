import type { App } from "@/context/apps-context-base";
import { emptyEditorState, newAddressRow, newGitSource } from "@/lib/app-editor-io";
import type { NewAppSource } from "@/lib/routes";
import type { AppEditorState } from "@/types/app-config";

const SOURCES: readonly NewAppSource[] = ["template", "compose", "git"];

// parseNewAppSource reads /new?from=: one of the three sources, else null
// (the chooser).
export function parseNewAppSource(raw: string | null): NewAppSource | null {
  return raw !== null && (SOURCES as readonly string[]).includes(raw) ? (raw as NewAppSource) : null;
}

// createdApp is the app a create added: named name and absent from the list
// before (another app may carry the same name). Undefined when the agent's
// answer doesn't list it yet.
export function createdApp(list: App[], before: ReadonlySet<string>, name: string): App | undefined {
  return list.find((a) => a.name === name && !before.has(a.id));
}

// composeStart is the compose form's state: the example compose.yml and one
// blank Address row (dropped on create while blank).
export function composeStart(): AppEditorState {
  const s = emptyEditorState();
  return { ...s, config: { ...s.config, ingress: { domains: [newAddressRow()], redirects: [] } } };
}

// gitStart is the Git form's state: no files of its own (the repository's
// compose file runs) and the default source.
export function gitStart(): AppEditorState {
  const s = emptyEditorState();
  return { ...s, config: { ...s.config, files: [], source: newGitSource() }, files: {} };
}
