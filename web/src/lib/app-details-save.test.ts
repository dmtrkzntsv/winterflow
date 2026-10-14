import { beforeEach, describe, expect, it, vi } from "vitest";

import type { AppDetailPayload } from "@/context/apps-context-base";
import { saveAppDetails, type AppDetailsDeps } from "@/lib/app-details-save";
import { ENCRYPTED } from "@/lib/ecies";

const detail: AppDetailPayload = {
  app: {
    app_id: "a1",
    config: btoa(
      JSON.stringify({ name: "Shop", description: "Old", icon: "store", color: "#112233", files: [{ filename: "compose.yml" }], variables: [] }),
    ),
    files: [{ name: "compose.yml", content: btoa("services: {}\n") }],
    variables: [],
  },
  revision: 1,
  available_revisions: null,
};
const before = { name: "Shop", icon: "store", color: "#112233", description: "Old" };

let mocks: {
  rename: ReturnType<typeof vi.fn>;
  getApp: ReturnType<typeof vi.fn>;
  getRevisions: ReturnType<typeof vi.fn>;
  getPublicKey: ReturnType<typeof vi.fn>;
  saveApp: ReturnType<typeof vi.fn>;
};
let deps: AppDetailsDeps;
function makeDeps(revisions = { current: "bbb", deployed: "bbb", revisions: [] }) {
  mocks = {
    rename: vi.fn(async () => {}),
    getApp: vi.fn(async () => detail),
    getRevisions: vi.fn(async () => revisions),
    getPublicKey: vi.fn(async () => "K"),
    saveApp: vi.fn(async () => []),
  };
  deps = mocks as unknown as AppDetailsDeps;
}

const saved = () => mocks.saveApp.mock.calls[0][0] as { draft: boolean; config: Record<string, unknown>; files: { name: string }[] };

describe("saveAppDetails", () => {
  beforeEach(() => makeDeps());

  it("renames through the rename route alone when only the name changed", async () => {
    expect(await saveAppDetails(deps, "a1", before, { ...before, name: "  Store " }, true)).toEqual({ renamed: true, saved: "none" });
    expect(mocks.rename).toHaveBeenCalledWith("a1", "Store");
    expect(mocks.getApp).not.toHaveBeenCalled();
    expect(mocks.saveApp).not.toHaveBeenCalled();
  });

  it("saves a running app's new colour with its files, and redeploys it", async () => {
    expect(await saveAppDetails(deps, "a1", before, { ...before, color: "#ff0000" }, true)).toEqual({ renamed: false, saved: "deployed" });
    expect(mocks.rename).not.toHaveBeenCalled();
    expect(saved().draft).toBe(false);
    expect(saved().config).toMatchObject({ name: "Shop", color: "#ff0000", icon: "store", description: "Old" });
    expect(saved().files.map((f) => f.name)).toEqual(["compose.yml"]);
  });

  it("keeps the change as a draft for a stopped app: a colour never starts it", async () => {
    expect((await saveAppDetails(deps, "a1", before, { ...before, description: "New" }, false)).saved).toBe("draft");
    expect(saved().draft).toBe(true);
    expect(saved().config).toMatchObject({ description: "New" });
  });

  it("keeps the change as a draft beside undeployed changes: it never deploys them", async () => {
    makeDeps({ current: "ccc", deployed: "bbb", revisions: [] });
    expect((await saveAppDetails(deps, "a1", before, { ...before, icon: "cart" }, true)).saved).toBe("draft");
    expect(saved().draft).toBe(true);
  });

  it("renames first, then saves only what changed", async () => {
    await saveAppDetails(deps, "a1", before, { ...before, name: "Store", icon: "cart" }, true);
    expect(mocks.rename.mock.invocationCallOrder[0]).toBeLessThan(mocks.getApp.mock.invocationCallOrder[0]);
    // The stored description is kept as it was read, never as the form had it.
    expect(saved().config).toMatchObject({ icon: "cart", description: "Old", color: "#112233" });
  });

  it("sends everything else in the app as stored: secrets as the keep placeholder, ingress and git source intact", async () => {
    const full: AppDetailPayload = {
      app: {
        app_id: "a1",
        config: btoa(
          JSON.stringify({
            name: "Shop",
            description: "Old",
            icon: "store",
            color: "#112233",
            files: [
              { filename: "compose.yml", is_encrypted: false },
              { filename: "key.pem", is_encrypted: true },
            ],
            variables: [
              { name: "PLAIN", is_encrypted: false },
              { name: "TOKEN", is_encrypted: true },
            ],
            ingress: {
              domains: [{ domain: "shop.example.com", upstream_port: 8080, ssl: true }],
              redirects: [{ domain: "www.example.com", to: "https://shop.example.com", code: 301, ssl: true }],
            },
            source: {
              repo_url: "https://git.example.com/shop.git",
              branch: "main",
              compose_path: "deploy/compose.yml",
              auto_update: true,
              poll_seconds: 300,
              token_set: true,
            },
          }),
        ),
        files: [
          { name: "compose.yml", content: btoa("services: {}\n") },
          { name: "key.pem", content: btoa("masked"), encrypted: true },
        ],
        variables: [
          { name: "PLAIN", content: btoa("one") },
          { name: "TOKEN", content: btoa("masked"), encrypted: true },
        ],
      },
      revision: 3,
      available_revisions: null,
    };
    mocks.getApp.mockResolvedValue(full);

    await saveAppDetails(deps, "a1", before, { ...before, icon: "cart", color: "#ff0000", description: "New" }, true);

    expect(mocks.getPublicKey).not.toHaveBeenCalled();
    const body = mocks.saveApp.mock.calls[0][0] as {
      app: Record<string, unknown>;
      config: Record<string, unknown>;
      files: { name: string; encrypted: boolean; content: string }[];
      variables: { name: string; encrypted: boolean; content: string }[];
      source: Record<string, unknown>;
    };
    expect(body.app).toMatchObject({ id: "a1", name: "Shop", icon: "cart", color: "#ff0000" });
    expect(body.config).toMatchObject({ icon: "cart", color: "#ff0000", description: "New" });
    expect(body.files).toEqual([
      { name: "compose.yml", encrypted: false, content: "services: {}\n" },
      { name: "key.pem", encrypted: true, content: ENCRYPTED },
    ]);
    expect(body.variables).toEqual([
      { name: "PLAIN", encrypted: false, content: "one" },
      { name: "TOKEN", encrypted: true, content: ENCRYPTED },
    ]);
    expect(body.config.ingress).toEqual({
      domains: [{ domain: "shop.example.com", upstream_port: 8080, ssl: true }],
      redirects: [{ domain: "www.example.com", to: "https://shop.example.com", code: 301, ssl: true }],
    });
    expect(body.source).toEqual({
      repo_url: "https://git.example.com/shop.git",
      branch: "main",
      compose_path: "deploy/compose.yml",
      auto_update: true,
      poll_seconds: 300,
      token: ENCRYPTED,
    });
    expect(body.config.source).toMatchObject({ token_set: true });
  });
});
