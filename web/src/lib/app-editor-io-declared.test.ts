import { describe, expect, it, vi } from "vitest";

import { buildSavePayload, editorErrors, stateFromDetail } from "@/lib/app-editor-io";
import { setDeclaredValue } from "@/lib/declared-variables";
import type { DeclaredVariable } from "@/types/app-config";

vi.mock("@/lib/ecies", async (original) => ({
  ...(await original<typeof import("@/lib/ecies")>()),
  encryptSecret: vi.fn(async (value: string, key: string) => `ct(${key}):${value}`),
}));

const b64 = (s: string) => btoa(s);
const BASE: DeclaredVariable = { name: "BASE_URL", required: true, encrypted: false, pattern: "https?://.+", default: "http://localhost", services: ["app"] };
const LANG: DeclaredVariable = { name: "LANGUAGE", required: false, encrypted: false, default: "en", services: ["app"] };
const TOKEN: DeclaredVariable = { name: "TOKEN", required: true, encrypted: true, default: null, services: ["app"] };

function loaded() {
  return stateFromDetail("app-1", {
    app: {
      app_id: "app-1",
      config: b64(JSON.stringify({ name: "notes", files: [{ filename: "compose.yml" }], variables: [{ name: "TOKEN", is_encrypted: true }, { name: "EXTRA" }] })),
      files: [{ name: "compose.yml", content: b64("services: {}") }],
      variables: [
        { name: "TOKEN", content: b64("<encrypted>"), encrypted: true },
        { name: "EXTRA", content: b64("1") },
      ],
    },
    declared_variables: [BASE, LANG, TOKEN],
    revision: 1,
    available_revisions: null,
  });
}

describe("app editor io: declared variables", () => {
  it("reads them from app.get (none from an older agent)", () => {
    expect(loaded().declared).toEqual([BASE, LANG, TOKEN]);
    const old = stateFromDetail("app-1", { app: { app_id: "app-1", config: b64("{}"), files: null, variables: null }, revision: 1, available_revisions: null });
    expect(old.declared).toEqual([]);
  });

  it("names a missing required value and a value breaking its rule, by decl:<NAME>", () => {
    const s = loaded();
    expect(editorErrors(s)).toEqual({ "decl:BASE_URL": "Enter a value: this variable is required." });
    expect(editorErrors(setDeclaredValue(s, BASE, "has$dollar"))).toEqual({ "decl:BASE_URL": "A plain value can't contain $." });
    expect(editorErrors(setDeclaredValue(s, BASE, "https://notes.example.com"))).toEqual({});
  });

  it("flags a name used twice on the free-form row, never on the declared one", async () => {
    const s = setDeclaredValue(loaded(), BASE, "https://notes.example.com");
    const extra = s.config.variables.find((v) => v.name === "EXTRA")!;
    const dup = { ...s, config: { ...s.config, variables: s.config.variables.map((v) => (v.id === extra.id ? { ...v, name: "BASE_URL" } : v)) } };
    expect(editorErrors(dup)).toEqual({ [`var:${extra.id}`]: "Another variable has this name." });
    // Loaded with two rows of one name: the first is the declared one.
    const twice = stateFromDetail("app-1", {
      app: {
        app_id: "app-1",
        config: b64(JSON.stringify({ name: "notes", files: [{ filename: "compose.yml" }], variables: [{ name: "BASE_URL" }, { name: "BASE_URL" }] })),
        files: [{ name: "compose.yml", content: b64("services: {}") }],
        variables: [
          { name: "BASE_URL", content: b64("https://a") },
          { name: "BASE_URL", content: b64("https://b") },
        ],
      },
      declared_variables: [BASE],
      revision: 1,
      available_revisions: null,
    });
    const [first, second] = twice.config.variables;
    expect(twice.declaredRows).toEqual({ BASE_URL: first.id });
    expect(editorErrors(twice)).toEqual({ [`var:${second.id}`]: "Another variable has this name." });
  });

  it("drops only the declared row when its value is empty", async () => {
    const s = loaded();
    s.config.variables.push({ id: "free", name: "LANGUAGE", is_encrypted: false });
    s.variables.free = "";
    const payload = await buildSavePayload(setDeclaredValue(setDeclaredValue(s, BASE, "https://x"), LANG, ""), async () => "PUB", "app-1");
    expect(payload.config.variables.map((v) => v.name)).toEqual(["TOKEN", "EXTRA", "LANGUAGE", "BASE_URL"]);
  });

  it("lets a draft leave a required value empty, nothing else", () => {
    const s = loaded();
    expect(editorErrors(s, true)).toEqual({});
    expect(editorErrors(setDeclaredValue(s, BASE, "has$dollar"), true)).toEqual({ "decl:BASE_URL": "A plain value can't contain $." });
  });

  it("never blocks a save on a pattern: JavaScript's engine is not RE2", async () => {
    const s = setDeclaredValue(setDeclaredValue(loaded(), BASE, "notes"), TOKEN, "x");
    expect(editorErrors(s)).toEqual({});
    const payload = await buildSavePayload(s, async () => "PUB", "app-1");
    expect(payload.variables).toContainEqual({ name: "BASE_URL", encrypted: false, content: "notes" });
  });

  it("sends a declared variable only with a value: empty, the compose file's own stands", async () => {
    const s = setDeclaredValue(setDeclaredValue(loaded(), BASE, "https://notes.example.com"), LANG, "");
    const payload = await buildSavePayload(s, async () => "PUB", "app-1");
    expect(payload.variables).toEqual([
      { name: "TOKEN", encrypted: true, content: "<encrypted>" },
      { name: "EXTRA", encrypted: false, content: "1" },
      { name: "BASE_URL", encrypted: false, content: "https://notes.example.com" },
    ]);
    expect(payload.config.variables.map((v) => v.name)).toEqual(["TOKEN", "EXTRA", "BASE_URL"]);
  });
});
