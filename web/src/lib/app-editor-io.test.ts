import { describe, expect, it, vi } from "vitest";

import {
  buildSavePayload,
  editorErrors,
  emptyEditorState,
  newAddressRow,
  newGitSource,
  pruneBlankAddresses,
  stateFromDetail,
} from "@/lib/app-editor-io";
import type { AppEditorState, AppFileMeta, AppVariableMeta } from "@/types/app-config";

const b64 = (s: string) => btoa(s);

vi.mock("@/lib/ecies", async (original) => ({
  ...(await original<typeof import("@/lib/ecies")>()),
  encryptSecret: vi.fn(async (value: string, key: string) => `ct(${key}):${value}`),
}));

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

describe("app editor io: stored secrets", () => {
  // A stored app with one plain and one secret variable, as app.get answers
  // (the agent masks the secret as the placeholder).
  const stored = () =>
    stateFromDetail("app-1", {
      app: {
        app_id: "app-1",
        config: b64(
          JSON.stringify({
            name: "demo",
            files: [{ filename: "compose.yml" }, { filename: "key.pem", is_encrypted: true }],
            variables: [{ name: "PORT" }, { name: "TOKEN", is_encrypted: true }],
          }),
        ),
        files: [
          { name: "compose.yml", content: b64("services: {}") },
          { name: "key.pem", content: b64("<encrypted>"), encrypted: true },
        ],
        variables: [
          { name: "PORT", content: b64("8080") },
          { name: "TOKEN", content: b64("<encrypted>"), encrypted: true },
        ],
      },
      revision: 1,
      available_revisions: null,
    });
  const tokenId = (s: AppEditorState) => s.config.variables[1].id;
  const keyId = (s: AppEditorState) => s.config.files[1].id;
  const withFile = (s: AppEditorState, patch: Partial<AppFileMeta>, content?: string): AppEditorState => {
    const id = keyId(s);
    return {
      ...s,
      config: { ...s.config, files: s.config.files.map((f) => (f.id === id ? { ...f, ...patch } : f)) },
      files: content === undefined ? s.files : { ...s.files, [id]: content },
    };
  };
  const withVar = (s: AppEditorState, patch: Partial<AppVariableMeta>, value?: string): AppEditorState => {
    const id = tokenId(s);
    return {
      ...s,
      config: { ...s.config, variables: s.config.variables.map((v) => (v.id === id ? { ...v, ...patch } : v)) },
      variables: value === undefined ? s.variables : { ...s.variables, [id]: value },
    };
  };
  const save = (s: AppEditorState) => buildSavePayload(s, async () => "PUB", "app-1");

  it("marks a secret read back from the agent as stored under its name", () => {
    const s = stored();
    expect(s.config.variables.map(({ name, is_encrypted, storedAs }) => ({ name, is_encrypted, storedAs }))).toEqual([
      { name: "PORT", is_encrypted: false, storedAs: undefined },
      { name: "TOKEN", is_encrypted: true, storedAs: "TOKEN" },
    ]);
    expect(s.config.files.map(({ filename, is_encrypted, storedAs }) => ({ filename, is_encrypted, storedAs }))).toEqual([
      { filename: "compose.yml", is_encrypted: false, storedAs: undefined },
      { filename: "key.pem", is_encrypted: true, storedAs: "key.pem" },
    ]);
  });

  it("sends an untouched stored secret file as the placeholder, without reading the key", async () => {
    let keyReads = 0;
    const payload = await buildSavePayload(stored(), async () => {
      keyReads++;
      return "PUB";
    }, "app-1");
    expect(payload.files).toEqual([
      { name: "compose.yml", encrypted: false, content: "services: {}" },
      { name: "key.pem", encrypted: true, content: "<encrypted>" },
    ]);
    expect(keyReads).toBe(0);
    expect(JSON.stringify(payload.config)).not.toContain("storedAs");
  });

  it("keeps a stored secret file left empty after Replace", async () => {
    const payload = await save(withFile(stored(), {}, ""));
    expect(payload.files[1]).toEqual({ name: "key.pem", encrypted: true, content: "<encrypted>" });
  });

  it("encrypts a stored secret file's new contents", async () => {
    const payload = await save(withFile(stored(), {}, "-----BEGIN KEY-----"));
    expect(payload.files[1]).toEqual({ name: "key.pem", encrypted: true, content: "ct(PUB):-----BEGIN KEY-----" });
  });

  it("never sends a stored secret file's placeholder as plaintext", async () => {
    // Turned plain with the placeholder still in it (the editor empties it).
    const plain = withFile(stored(), { is_encrypted: false });
    expect(editorErrors(plain)).toEqual({ [`file:${keyId(plain)}:content`]: "A file can't start with <encrypted>." });
    await expect(save(plain)).rejects.toThrow("key.pem: A file can't start with <encrypted>.");
    // Turned plain and emptied: sent as an empty plain file.
    const emptied = withFile(stored(), { is_encrypted: false }, "");
    expect(editorErrors(emptied)).toEqual({});
    expect((await save(emptied)).files[1]).toEqual({ name: "key.pem", encrypted: false, content: "" });
  });

  it("asks a renamed stored secret file for its contents again", async () => {
    for (const content of ["", "<encrypted>"]) {
      const renamed = withFile(stored(), { filename: "tls.pem" }, content);
      expect(editorErrors(renamed)).toEqual({
        [`file:${keyId(renamed)}:content`]: "Paste the contents again: a renamed secret file doesn't keep its old contents.",
      });
      await expect(save(renamed)).rejects.toThrow("tls.pem: Paste the contents again");
    }
    const given = await save(withFile(stored(), { filename: "tls.pem" }, "new"));
    expect(given.files[1]).toEqual({ name: "tls.pem", encrypted: true, content: "ct(PUB):new" });
    // Renamed back, it stands for the stored file again.
    const back = withFile(stored(), { filename: " key.pem " }, "");
    expect(editorErrors(back)).toEqual({});
    expect((await save(back)).files[1]).toEqual({ name: "key.pem", encrypted: true, content: "<encrypted>" });
  });

  it("refuses the placeholder in a new secret file", async () => {
    const fresh = emptyEditorState();
    const added: AppEditorState = {
      ...fresh,
      config: { ...fresh.config, files: [...fresh.config.files, { id: "f1", filename: "a.key", is_encrypted: true }] },
      files: { ...fresh.files, f1: "<encrypted>" },
    };
    expect(editorErrors(added)).toMatchObject({ "file:f1:content": "A file can't start with <encrypted>." });
    await expect(buildSavePayload(added, async () => "PUB")).rejects.toThrow("a.key: A file can't start with <encrypted>.");
  });

  it("keeps a stored secret variable left empty after Replace", async () => {
    let keyReads = 0;
    const payload = await buildSavePayload(withVar(stored(), {}, ""), async () => {
      keyReads++;
      return "PUB";
    }, "app-1");
    expect(payload.variables[1]).toEqual({ name: "TOKEN", encrypted: true, content: "<encrypted>" });
    expect(keyReads).toBe(0);
  });

  it("asks a renamed stored secret variable for a new value", async () => {
    for (const value of ["", "<encrypted>"]) {
      const renamed = withVar(stored(), { name: "API_TOKEN" }, value);
      expect(editorErrors(renamed)).toEqual({
        [`var:${tokenId(renamed)}:value`]: "Enter a value: a renamed secret doesn't keep its old one.",
      });
      await expect(save(renamed)).rejects.toThrow("API_TOKEN: Enter a value");
    }
    const given = await save(withVar(stored(), { name: "API_TOKEN" }, "s3cret"));
    expect(given.variables[1]).toEqual({ name: "API_TOKEN", encrypted: true, content: "ct(PUB):s3cret" });
    // A stored secret turned plain and emptied is a plain empty value.
    const plain = await save(withVar(stored(), { is_encrypted: false }, ""));
    expect(plain.variables[1]).toEqual({ name: "TOKEN", encrypted: false, content: "" });
  });

  it("sends an untouched stored secret as the placeholder, without reading the key", async () => {
    const s = stored();
    let keyReads = 0;
    const payload = await buildSavePayload(s, async () => {
      keyReads++;
      return "PUB";
    }, "app-1");
    expect(payload.variables).toEqual([
      { name: "PORT", encrypted: false, content: "8080" },
      { name: "TOKEN", encrypted: true, content: "<encrypted>" },
    ]);
    expect(keyReads).toBe(0);
    // The editor-only flag never rides the config.
    expect(JSON.stringify(payload.config)).not.toContain("stored");
  });

  it("sends a replaced secret as a new ciphertext, never the placeholder", async () => {
    const s = stored();
    const replaced = { ...s, variables: { ...s.variables, [tokenId(s)]: "hunter2" } };
    const payload = await buildSavePayload(replaced, async () => "PUB", "app-1");
    const token = payload.variables.find((v) => v.name === "TOKEN")!;
    expect(token).toEqual({ name: "TOKEN", encrypted: true, content: "ct(PUB):hunter2" });
    expect(JSON.stringify(payload.variables)).not.toContain("<encrypted>");
    expect(JSON.stringify(payload)).not.toContain('"hunter2"');
  });

  it("refuses a value that starts with the placeholder", async () => {
    const s = stored();
    const id = tokenId(s);
    const tampered = { ...s, variables: { ...s.variables, [id]: "<encrypted>x" } };
    expect(editorErrors(tampered)).toEqual({ [`var:${id}:value`]: "A value can't start with <encrypted>." });
    await expect(buildSavePayload(tampered, async () => "PUB", "app-1")).rejects.toThrow(
      "TOKEN: A value can't start with <encrypted>.",
    );
  });

  it("refuses the bare placeholder where no secret is stored", () => {
    const s = stored();
    const portId = s.config.variables.find((v) => v.name === "PORT")!.id;
    const plain = { ...s, variables: { ...s.variables, [portId]: "<encrypted>" } };
    expect(editorErrors(plain)).toEqual({ [`var:${portId}:value`]: "A value can't start with <encrypted>." });
    const fresh = emptyEditorState();
    const added = {
      ...fresh,
      config: { ...fresh.config, variables: [{ id: "v1", name: "NEW", is_encrypted: true }] },
      variables: { v1: "<encrypted>" },
    };
    expect(editorErrors(added)).toMatchObject({ "var:v1:value": "A value can't start with <encrypted>." });
    expect(editorErrors(stored())).toEqual({});
  });
});
