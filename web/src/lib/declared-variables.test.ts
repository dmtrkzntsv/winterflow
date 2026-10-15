import { describe, expect, it } from "vitest";

import { emptyEditorState } from "@/lib/app-editor-io";
import { declaredPatternHint, declaredRow, declaredRowsOf, declaredValueError, matchesPattern, setDeclaredValue } from "@/lib/declared-variables";
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

describe("declaredRow", () => {
  it("is the row fixed at load or by the form, never one whose name is typed to match", () => {
    const start = emptyEditorState();
    start.config.variables = [{ id: "a", name: "OTHER", is_encrypted: false }];
    start.declaredRows = declaredRowsOf([BASE], start.config.variables);
    expect(declaredRow(start, BASE)).toBeUndefined();
    // A free-form row renamed to the declared name stays a free-form row.
    const typed = { ...start, config: { ...start.config, variables: [{ id: "a", name: "BASE_URL", is_encrypted: false }] } };
    expect(declaredRow(typed, BASE)).toBeUndefined();
    // The form's first value adds the declared row, which it then keeps.
    const set = setDeclaredValue(typed, BASE, "https://x");
    const row = declaredRow(set, BASE);
    expect(row?.id).not.toBe("a");
    expect(declaredRow(setDeclaredValue(set, BASE, "https://y"), BASE)?.id).toBe(row?.id);
  });
});

describe("declaredValueError", () => {
  it("mirrors the server's rules", () => {
    expect(declaredValueError(BASE, "", false)).toBe("Enter a value: this variable is required.");
    expect(declaredValueError(BASE, "notes", false)).toBe("");
    expect(declaredValueError(BASE, "https://x/$y", false)).toBe("A plain value can't contain $.");
    expect(declaredValueError(BASE, "https://notes.example.com", false)).toBe("");
    expect(declaredValueError(TOKEN, "", false)).toBe("");
    expect(declaredValueError(TOKEN, "pa$1", false)).toBe("");
    expect(declaredValueError(TOKEN, "1234", false)).toBe("");
    expect(declaredValueError({ ...TOKEN, required: true }, "<encrypted>", true)).toBe("");
  });
});

describe("declaredPatternHint", () => {
  it("only hints, never errors: a mismatch is the server's call", () => {
    expect(declaredPatternHint(BASE, "notes", false)).toBe("May not match https?://.+; the server checks it on save.");
    expect(declaredPatternHint(TOKEN, "pa$1", false)).toBe("May not match [0-9]{4}; the server checks it on save.");
    expect(declaredPatternHint(BASE, "https://notes.example.com", false)).toBe("");
    expect(declaredPatternHint(BASE, "", false)).toBe("");
    expect(declaredPatternHint({ ...TOKEN, required: true }, "<encrypted>", true)).toBe("");
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
    start.declaredRows = declaredRowsOf([TOKEN], start.config.variables);
    const s = setDeclaredValue(start, TOKEN, "1234");
    expect(s.config.variables[0]).toMatchObject({ id: "v", is_encrypted: true });
    expect(s.variables.v).toBe("1234");
  });
});
