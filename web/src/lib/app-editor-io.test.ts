import { describe, expect, it } from "vitest";

import {
  buildSavePayload,
  editorErrors,
  emptyEditorState,
  newAddressRow,
  newGitSource,
  pruneBlankAddresses,
  stateFromDetail,
} from "@/lib/app-editor-io";
import type { AppEditorState } from "@/types/app-config";

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

function withConfig(patch: Partial<AppEditorState["config"]>): AppEditorState {
  const s = emptyEditorState();
  return { ...s, config: { ...s.config, name: "shop", ...patch } };
}

describe("editorErrors", () => {
  it("has nothing to say about a named app with a compose file", () => {
    expect(editorErrors(withConfig({}))).toEqual({});
  });

  it("names every problem by its field", () => {
    const s = withConfig({
      name: " ",
      files: [{ id: "f1", filename: "", is_encrypted: false }],
      variables: [{ id: "v1", name: "", is_encrypted: false }],
    });
    expect(editorErrors(s)).toEqual({
      name: "Give the app a name.",
      files: "Add a compose file named compose.yml.",
      "file:f1": "Name this file.",
      "var:v1": "Name this variable.",
    });
  });

  it("takes every compose file name compose itself finds", () => {
    for (const filename of ["compose.yaml", "compose.yml", "docker-compose.yml", "docker-compose.yaml"]) {
      expect(editorErrors(withConfig({ files: [{ id: "f", filename, is_encrypted: false }] }))).toEqual({});
    }
  });

  it("lets a Git-deployed app go without a compose file but asks for its repository and branch", () => {
    const s = withConfig({ files: [], source: { ...newGitSource(), branch: " " } });
    expect(editorErrors(s)).toEqual({ repo_url: "Enter the repository's URL.", branch: "Enter a branch." });
    expect(newGitSource()).toEqual({ repo_url: "", branch: "main", compose_path: "", auto_update: true, poll_seconds: 120 });
  });

  it("checks addresses: hostname, duplicates and port", () => {
    const s = withConfig({
      ingress: {
        domains: [
          { id: "d1", domain: "", upstream_port: "", ssl: true },
          { id: "d2", domain: "Shop.Example.com", upstream_port: 8080, ssl: true },
          { id: "d3", domain: "shop.example.com", upstream_port: 70000, ssl: true },
          { id: "d4", domain: "shop.example.com", upstream_port: 80, ssl: true },
        ],
        redirects: [],
      },
    });
    expect(editorErrors(s)).toEqual({
      "domain:d1": "Enter a hostname.",
      "port:d1": "Enter a port from 1 to 65535.",
      "domain:d2": "Use a lowercase hostname like app.example.com.",
      "port:d3": "Enter a port from 1 to 65535.",
      "domain:d4": "This hostname is already used above.",
    });
  });

  it("checks redirects field by field, in the order the server reads them", () => {
    const red = (id: string, patch: Partial<{ domain: string; path: string; to: string; code: 301 | 302 | 307 | 308 }>) => ({
      id,
      domain: "old.example.com",
      path: "",
      to: "https://new.example.com",
      code: 301 as const,
      ssl: true,
      ...patch,
    });
    const s = withConfig({
      ingress: {
        domains: [],
        redirects: [
          red("r1", { path: "/a", domain: "late.example.com" }),
          red("r2", { domain: "late.example.com" }),
          red("r3", { to: "http:/nohost" }),
          red("r4", { domain: "r4.example.com", to: "https://x.example.com/{env.SECRET}" }),
          red("r5", { domain: "late.example.com", path: "no-slash" }),
          red("r6", { domain: "late.example.com", path: "/{x}" }),
          red("r7", { domain: "other.example.com", code: 303 as unknown as 301 }),
        ],
      },
    });
    expect(editorErrors(s)).toEqual({
      "redirect-host:r1": "Add late.example.com as an address or a whole-domain redirect above this rule first.",
      "redirect-to:r3": "Send it to a full address starting with http:// or https://.",
      "redirect-to:r4": "The target can't contain { or }.",
      "redirect-path:r5": "The path must start with /.",
      "redirect-path:r6": "The path can't contain { or }.",
      "redirect-to:r7": "Pick 301, 302, 307 or 308.",
    });
  });
});

describe("pruneBlankAddresses", () => {
  it("drops untouched rows and the ingress they leave empty", () => {
    const blank = { ...newAddressRow(), id: "d1" };
    const s = withConfig({ ingress: { domains: [blank], redirects: [] } });
    expect(pruneBlankAddresses(s).config.ingress).toBeUndefined();
    expect(blank).toMatchObject({ domain: "", upstream_port: "", ssl: true });
  });

  it("keeps a row with anything in it", () => {
    const s = withConfig({
      ingress: {
        domains: [
          { id: "d1", domain: "", upstream_port: "", ssl: true },
          { id: "d2", domain: "", upstream_port: 8080, ssl: true },
        ],
        redirects: [{ id: "r1", domain: "", path: "", to: "https://x.example.com", code: 301, ssl: true }],
      },
    });
    const out = pruneBlankAddresses(s).config.ingress;
    expect(out?.domains.map((d) => d.id)).toEqual(["d2"]);
    expect(out?.redirects.map((r) => r.id)).toEqual(["r1"]);
  });

  it("leaves a state without ingress as it is", () => {
    const s = withConfig({});
    expect(pruneBlankAddresses(s)).toBe(s);
  });
});
