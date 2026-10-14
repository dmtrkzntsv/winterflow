// Adding and removing files and variables in the app editor's state: a row
// and its content (keyed by the row's local id) always move together.
import { localId, type AppEditorState, type AppFileMeta } from "@/types/app-config";

type Files = { files: AppFileMeta[]; contents: Record<string, string> };

export function addFile(files: AppFileMeta[], contents: Record<string, string>): Files {
  const id = localId("file");
  return { files: [...files, { id, filename: "", is_encrypted: false }], contents: { ...contents, [id]: "" } };
}

export function removeFile(files: AppFileMeta[], contents: Record<string, string>, id: string): Files {
  const rest = { ...contents };
  delete rest[id];
  return { files: files.filter((f) => f.id !== id), contents: rest };
}

export function addVariable(state: AppEditorState): AppEditorState {
  const id = localId("var");
  return {
    ...state,
    config: { ...state.config, variables: [...state.config.variables, { id, name: "", is_encrypted: false }] },
    variables: { ...state.variables, [id]: "" },
  };
}

export function removeVariable(state: AppEditorState, id: string): AppEditorState {
  const rest = { ...state.variables };
  delete rest[id];
  return {
    ...state,
    config: { ...state.config, variables: state.config.variables.filter((v) => v.id !== id) },
    variables: rest,
  };
}
