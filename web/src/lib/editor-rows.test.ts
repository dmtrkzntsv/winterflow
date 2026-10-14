import { describe, expect, it } from "vitest";

import { emptyEditorState } from "@/lib/app-editor-io";
import { addFile, addVariable, removeFile, removeVariable } from "@/lib/editor-rows";

describe("editor rows", () => {
  it("adds and removes a file with its content", () => {
    const files = [{ id: "f1", filename: "compose.yml", is_encrypted: false }];
    const added = addFile(files, { f1: "x" });
    expect(added.files).toHaveLength(2);
    const id = added.files[1].id;
    expect(added.files[1]).toMatchObject({ filename: "", is_encrypted: false });
    expect(added.contents).toEqual({ f1: "x", [id]: "" });
    expect(removeFile(added.files, added.contents, "f1")).toEqual({ files: [added.files[1]], contents: { [id]: "" } });
  });

  it("adds and removes a variable with its value", () => {
    const s = addVariable(emptyEditorState());
    const [v] = s.config.variables;
    expect(v).toMatchObject({ name: "", is_encrypted: false });
    expect(s.variables).toEqual({ [v.id]: "" });
    const back = removeVariable(s, v.id);
    expect(back.config.variables).toEqual([]);
    expect(back.variables).toEqual({});
  });
});
