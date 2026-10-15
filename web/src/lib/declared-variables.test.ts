import { describe, expect, it } from "vitest";

import { emptyEditorState } from "@/lib/app-editor-io";
import { declaredRow, declaredValueError, matchesPattern, setDeclaredValue } from "@/lib/declared-variables";
import type { DeclaredVariable } from "@/types/app-config";

const BASE: DeclaredVariable = { name: "BASE_URL", required: true, encrypted: false, pattern: "https?://.+", default: "http://localhost", services: ["app"] };
const TOKEN: DeclaredVariable = { name: "TOKEN", required: false, encrypted: true, pattern: "[0-9]{4}", default: null, services: ["app"] };

describe("matchesPattern", () => {
  it("matches the whole value, and leaves patterns JavaScript can't compile to the server", () => {
    expect(matchesPattern("[a-z]{2}", "en")).toBe(true);
    expect(matchesPattern("[a-z]{2}", "eng")).toBe(false);
    expect(matchesPattern("a|b", "ab")).toBe(false);
    expect(matchesPattern(undefined, "anything")).toBe(true);
    expect(matchesPattern("(", "x")).toBe(true);
  });

  it("never rejects what RE2 would accept: RE2-only syntax is left to the server", () => {
    for (const pattern of ["\\p{L}+", "\\pL+", "\\P{N}+", "[[:alpha:]]+", "\\A[a-z]+\\z", "\\Qabc\\E", "\\x{61}bc", "(?i)ABC", "(?P<w>[a-z]+)"]) {
      expect(matchesPattern(pattern, "abc"), pattern).toBe(true);
    }
    expect(matchesPattern("[0-9]+", "abc")).toBe(false);
  });
});

describe("declaredValueError", () => {
  it("mirrors the server's rules", () => {
    expect(declaredValueError(BASE, "", false)).toBe("Enter a value: this variable is required.");
    expect(declaredValueError(BASE, "notes", false)).toBe("Must match https?://.+.");
    expect(declaredValueError(BASE, "https://x/$y", false)).toBe("A plain value can't contain $.");
    expect(declaredValueError(BASE, "https://notes.example.com", false)).toBe("");
    expect(declaredValueError(TOKEN, "", false)).toBe("");
    expect(declaredValueError(TOKEN, "pa$1", false)).toBe("Must match [0-9]{4}.");
    expect(declaredValueError(TOKEN, "1234", false)).toBe("");
    expect(declaredValueError({ ...TOKEN, required: true }, "<encrypted>", true)).toBe("");
  });
});

describe("setDeclaredValue", () => {
  it("adds the variable's row once, secret when the variable is, then updates it", () => {
    let s = setDeclaredValue(emptyEditorState(), TOKEN, "12");
    const row = declaredRow(s, TOKEN);
    expect(row).toMatchObject({ name: "TOKEN", is_encrypted: true });
    s = setDeclaredValue(s, TOKEN, "1234");
    expect(s.config.variables).toHaveLength(1);
    expect(s.variables[row!.id]).toBe("1234");
  });

  it("turns an existing plain row of a secret variable secret", () => {
    const start = emptyEditorState();
    start.config.variables = [{ id: "v", name: "TOKEN", is_encrypted: false }];
    start.variables = { v: "old" };
    const s = setDeclaredValue(start, TOKEN, "1234");
    expect(s.config.variables[0]).toMatchObject({ id: "v", is_encrypted: true });
    expect(s.variables.v).toBe("1234");
  });
});
