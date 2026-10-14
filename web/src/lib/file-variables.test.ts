import { describe, expect, it } from "vitest";

import { looksSecret, referencedNames, undeclaredNames, withVariables } from "@/lib/file-variables";
import { emptyEditorState } from "@/lib/app-editor-io";
import type { AppEditorState } from "@/types/app-config";

describe("referencedNames", () => {
  it("reads braced names without a default, once each, skipping compose's escape", () => {
    const compose = [
      "image: app:${VERSION}",
      "title: ${NOTES_TITLE} ${NOTES_TITLE}",
      "pass: ${REDIS_PASSWORD:?set it}",
      "a: ${WITH_DEFAULT:-x} ${ALT-y} ${PLUS:+z} ${PLUS2+z}",
      "b: $${ESCAPED} $$${AFTER_ESCAPE} $PLAIN",
    ].join("\n");
    expect(referencedNames([compose, "x: ${VERSION} ${OTHER}"])).toEqual([
      "VERSION",
      "NOTES_TITLE",
      "REDIS_PASSWORD",
      "AFTER_ESCAPE",
      "OTHER",
    ]);
  });
});

describe("undeclaredNames", () => {
  const state = (): AppEditorState => {
    const s = emptyEditorState();
    return {
      ...s,
      config: {
        ...s.config,
        files: [
          { id: "f1", filename: "compose.yml", is_encrypted: false },
          { id: "f2", filename: ".env", is_encrypted: false },
          { id: "f3", filename: "secret.yml", is_encrypted: true },
        ],
        variables: [{ id: "v1", name: " TITLE ", is_encrypted: false }],
      },
      files: {
        f1: "${TITLE} ${TZ} ${WF_APP_DIR} ${WF_STORAGE_DISK_PATH} ${COMPOSE_PROJECT_NAME} ${TOKEN} ${PORT}",
        f2: "${IN_ENV}",
        f3: "${IN_SECRET}",
      },
    };
  };

  it("leaves out what the app, the shared variables and the agent set, and files compose doesn't read", () => {
    expect(undeclaredNames(state(), [{ name: "TZ", scope: "org" }])).toEqual(["TOKEN", "PORT"]);
  });

  it("adds empty rows, secret where the name reads like one", () => {
    const next = withVariables(state(), ["TOKEN", "PORT"]);
    const added = next.config.variables.slice(1);
    expect(added.map((v) => [v.name, v.is_encrypted])).toEqual([
      ["TOKEN", true],
      ["PORT", false],
    ]);
    expect(added.map((v) => next.variables[v.id])).toEqual(["", ""]);
    expect(undeclaredNames(next, [{ name: "TZ", scope: "org" }])).toEqual([]);
  });

  it("knows a secret-looking name", () => {
    expect(["DB_PASSWORD", "API_KEY", "jwt_secret", "GH_TOKEN"].every(looksSecret)).toBe(true);
    expect(looksSecret("TITLE")).toBe(false);
  });
});
