import { describe, expect, it } from "vitest";

import {
  ADVANCED_FIELDS,
  DRAFT_FIELDS,
  draftFrom,
  draftProblem,
  endpointError,
  pathStyleDefault,
  storageBody,
  storageVar,
  STORAGE_TYPES,
  type Storage,
  type StorageDraft,
} from "@/lib/storages";

const s3: Storage = {
  name: "hetzner",
  type: "s3",
  s3: {
    endpoint: "https://fsn1.example.com",
    region: "fsn1",
    bucket: "tenants",
    prefix: "wf/",
    access_key_id: "AKIA",
    secret_access_key: "<encrypted>",
    path_style: true,
  },
  location: "https://fsn1.example.com/tenants/wf/",
  apps: ["a1"],
};

const draft = (over: Partial<StorageDraft>): StorageDraft => ({ ...draftFrom(null), name: "disk", path: "/mnt/usb", ...over });

// draftError is draftProblem's message ("" when the draft can be sent).
const draftError = (d: StorageDraft, editing: boolean) => draftProblem(d, editing)?.message ?? "";

describe("storages", () => {
  it("builds a draft from a listed storage, never with its secret", () => {
    expect(draftFrom(s3)).toEqual({
      name: "hetzner",
      type: "s3",
      path: "",
      endpoint: "https://fsn1.example.com",
      region: "fsn1",
      bucket: "tenants",
      prefix: "wf/",
      accessKeyId: "AKIA",
      secret: "",
      pathStyle: true,
    });
    expect(draftFrom({ name: "d", type: "local", local: { path: "/mnt/d" }, location: "/mnt/d", apps: [] }).path).toBe("/mnt/d");
    expect(draftFrom(null)).toMatchObject({ name: "", type: "local", pathStyle: false });
  });

  it("builds the API body per type", () => {
    expect(storageBody(draft({ name: " disk ", path: " /mnt/usb " }), "unused")).toEqual({
      name: "disk",
      type: "local",
      local: { path: "/mnt/usb" },
    });
    expect(storageBody(draftFrom(s3), "CIPHERTEXT")).toEqual({
      name: "hetzner",
      type: "s3",
      s3: {
        endpoint: "https://fsn1.example.com",
        region: "fsn1",
        bucket: "tenants",
        prefix: "wf/",
        access_key_id: "AKIA",
        secret_access_key: "CIPHERTEXT",
        path_style: true,
      },
    });
  });

  it("checks names like the agent", () => {
    for (const ok of ["a", "backup-disk", "s3", "x".repeat(32)]) expect(draftError(draft({ name: ok }), false)).toBe("");
    for (const bad of ["", "-a", "a-", "A", "a_b", "a.b", "x".repeat(33)]) {
      expect(draftProblem(draft({ name: bad }), false)).toEqual({
        field: "name",
        message: "Use 1-32 lowercase letters, digits and inner hyphens, like backup-disk.",
      });
    }
  });

  it("checks an endpoint's syntax, leaving plain http to the agent", () => {
    for (const ok of ["https://s3.example.com", "https://s3.example.com/", "http://127.0.0.1:9000", "http://localhost:9000", "http://s3.example.com"]) {
      expect(endpointError(ok)).toBe("");
    }
    expect(endpointError("https://s3.example.com/bucket")).toMatch(/without a path/);
    expect(endpointError("ftp://s3.example.com")).toMatch(/URL like/);
    expect(endpointError("not a url")).toMatch(/URL like/);
  });

  it("reports the first problem of a draft", () => {
    expect(draftError(draft({}), false)).toBe("");
    expect(draftError(draft({ name: "Disk" }), false)).toMatch(/lowercase/);
    expect(draftError(draft({ path: "backups" }), false)).toMatch(/absolute/);
    expect(draftError(draft({ path: "/" }), false)).toMatch(/cannot be \//);
    const ok = { ...draftFrom(s3), secret: "s3cret" };
    expect(draftError(ok, false)).toBe("");
    expect(draftError({ ...ok, endpoint: "s3.example.com" }, false)).toMatch(/URL like/);
    expect(draftError({ ...ok, bucket: "Bad_Bucket" }, false)).toMatch(/bucket/);
    expect(draftError({ ...ok, bucket: "a..b" }, false)).toMatch(/bucket/);
    for (const prefix of ["/x", "a//b", "a/../b", "./a"]) expect(draftError({ ...ok, prefix }, false)).toMatch(/folder names/);
    expect(draftError({ ...ok, prefix: "" }, false)).toBe("");
    expect(draftError({ ...ok, accessKeyId: " " }, false)).toMatch(/access key/);
    // A new storage needs its secret; an edit keeps the stored one.
    expect(draftError({ ...ok, secret: "" }, false)).toMatch(/secret/);
    expect(draftError({ ...ok, secret: "" }, true)).toBe("");
  });

  it("ties each problem to its field", () => {
    expect(draftProblem(draft({}), false)).toBeNull();
    expect(draftProblem(draft({ name: "" }), false)?.field).toBe("name");
    expect(draftProblem(draft({ path: "" }), false)?.field).toBe("path");
    const ok = { ...draftFrom(s3), name: "offsite", secret: "s3cret" };
    expect(draftProblem({ ...ok, endpoint: "" }, false)?.field).toBe("endpoint");
    expect(draftProblem({ ...ok, bucket: "" }, false)?.field).toBe("bucket");
    expect(draftProblem({ ...ok, prefix: "/x" }, false)?.field).toBe("prefix");
    expect(draftProblem({ ...ok, accessKeyId: "" }, false)?.field).toBe("accessKeyId");
    expect(draftProblem({ ...ok, secret: "" }, false)?.field).toBe("secret");
  });

  it("refuses to add a name the server already has, but edits it", () => {
    expect(draftProblem(draft({ name: "disk" }), false, ["disk", "hetzner"])).toEqual({
      field: "name",
      message: "A storage named disk already exists. Edit it instead.",
    });
    expect(draftProblem(draft({ name: "disk" }), true, ["disk"])).toBeNull();
    expect(draftProblem(draft({ name: "usb" }), false, ["disk"])).toBeNull();
  });

  it("turns path-style on for an IP address or localhost", () => {
    for (const on of ["http://127.0.0.1:9000", "http://192.168.1.5:9000", "http://localhost:9000", "http://[::1]:9000", "https://10.0.0.2"]) {
      expect(pathStyleDefault(on)).toBe(true);
    }
    for (const off of ["https://s3.example.com", "https://fsn1.your-objectstorage.com", "", "not a url"]) {
      expect(pathStyleDefault(off)).toBe(false);
    }
  });

  it("caps lengths in bytes like the agent", () => {
    // 512 bytes is the cap of a folder path, counted in UTF-8 like Go's len.
    expect(draftError(draft({ path: "/" + "a".repeat(511) }), false)).toBe("");
    expect(draftError(draft({ path: "/" + "a".repeat(512) }), false)).toMatch(/at most 512 bytes/);
    expect(draftError(draft({ path: "/" + "é".repeat(256) }), false)).toMatch(/at most 512 bytes/);
    const ok = { ...draftFrom(s3), secret: "s3cret" };
    const host = (n: number) => `https://${"a".repeat(n - 12)}.com`;
    expect(draftError({ ...ok, endpoint: host(255) }, false)).toBe("");
    expect(draftError({ ...ok, endpoint: `${host(255)}/` }, false)).toBe("");
    expect(draftError({ ...ok, endpoint: host(256) }, false)).toMatch(/at most 255 bytes/);
    expect(draftError({ ...ok, prefix: "p/".repeat(256) }, false)).toBe("");
    expect(draftError({ ...ok, prefix: "p/".repeat(256) + "x" }, false)).toMatch(/folder names/);
    // 512 as typed, 513 once the agent adds the trailing "/".
    expect(draftError({ ...ok, prefix: "p/".repeat(255) + "pp" }, false)).toMatch(/folder names/);
  });

  it("refuses control characters like the agent", () => {
    for (const c of ["\u0000", "\n", "\t", "\u001b", "\u007f"]) {
      expect(draftError(draft({ path: `/mnt/a${c}b` }), false)).toMatch(/control characters/);
      expect(draftError({ ...draftFrom(s3), secret: "s3cret", accessKeyId: `AK${c}IA` }, false)).toMatch(/control characters/);
    }
  });

  it("names compose variables like the agent", () => {
    expect(storageVar("backup-disk", "PATH")).toBe("WF_STORAGE_BACKUP_DISK_PATH");
  });
});

describe("storage form order", () => {
  it("asks for the S3 basics first and the advanced fields last", () => {
    expect(DRAFT_FIELDS).toEqual(["name", "path", "endpoint", "bucket", "accessKeyId", "secret", "region", "prefix"]);
    expect(ADVANCED_FIELDS).toEqual(["region", "prefix"]);
    // A missing secret is reported before a bad prefix: it comes first on screen.
    const ok = { ...draftFrom(s3), name: "offsite", secret: "s3cret" };
    expect(draftProblem({ ...ok, secret: "", prefix: "/x" }, false)?.field).toBe("secret");
  });

  it("names the two kinds in plain words", () => {
    expect(STORAGE_TYPES).toEqual({ local: "Folder on this server", s3: "S3 bucket" });
  });
});
