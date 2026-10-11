import { describe, expect, it } from "vitest";

import { buildSavePayload, stateFromDetail } from "@/lib/app-editor-io";

const b64 = (s: string) => btoa(s);

describe("app editor io: storage variables", () => {
  it("reads them from app.get and never sends them back", async () => {
    const state = stateFromDetail("app-1", {
      app: {
        app_id: "app-1",
        config: b64(JSON.stringify({ name: "demo", files: [{ filename: "compose.yml" }], variables: [{ name: "PORT" }] })),
        files: [{ name: "compose.yml", content: b64("x: ${WF_STORAGE_DISK_PATH}") }],
        variables: [{ name: "PORT", content: b64("8080") }],
      },
      storage_variables: [{ name: "WF_STORAGE_DISK_PATH", storage: "disk", secret: false }],
      revision: 1,
      available_revisions: null,
    });
    expect(state.storageVariables).toEqual([{ name: "WF_STORAGE_DISK_PATH", storage: "disk", secret: false }]);

    const payload = await buildSavePayload(state, async () => "PUB", "app-1");
    expect(payload.variables).toEqual([{ name: "PORT", encrypted: false, content: "8080" }]);
    expect(payload).not.toHaveProperty("storageVariables");
    expect(payload).not.toHaveProperty("storage_variables");
  });

  it("defaults to none for an older agent", () => {
    const state = stateFromDetail("app-1", {
      app: { app_id: "app-1", config: b64("{}"), files: null, variables: null },
      revision: 1,
      available_revisions: null,
    });
    expect(state.storageVariables).toEqual([]);
  });
});
