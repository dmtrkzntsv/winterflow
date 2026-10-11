import { describe, expect, it } from "vitest";

import {
  draftError,
  draftFrom,
  endpointError,
  storageBody,
  storageNameError,
  storageVar,
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
    for (const ok of ["a", "backup-disk", "s3", "x".repeat(32)]) expect(storageNameError(ok)).toBe("");
    for (const bad of ["", "-a", "a-", "A", "a_b", "a.b", "x".repeat(33)]) expect(storageNameError(bad)).not.toBe("");
  });

  it("checks endpoints like the agent", () => {
    for (const ok of ["https://s3.example.com", "https://s3.example.com/", "http://127.0.0.1:9000", "http://192.168.1.5:9000", "http://10.0.0.2", "http://172.20.0.1", "http://localhost:9000"]) {
      expect(endpointError(ok)).toBe("");
    }
    expect(endpointError("http://s3.example.com")).toMatch(/Use https/);
    expect(endpointError("http://172.32.0.1")).toMatch(/Use https/);
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
    expect(draftError({ ...ok, endpoint: "http://s3.example.com" }, false)).toMatch(/https/);
    expect(draftError({ ...ok, bucket: "Bad_Bucket" }, false)).toMatch(/bucket/);
    expect(draftError({ ...ok, bucket: "a..b" }, false)).toMatch(/bucket/);
    for (const prefix of ["/x", "a//b", "a/../b", "./a"]) expect(draftError({ ...ok, prefix }, false)).toMatch(/folder names/);
    expect(draftError({ ...ok, prefix: "" }, false)).toBe("");
    expect(draftError({ ...ok, accessKeyId: " " }, false)).toMatch(/access key/);
    // A new storage needs its secret; an edit keeps the stored one.
    expect(draftError({ ...ok, secret: "" }, false)).toMatch(/secret/);
    expect(draftError({ ...ok, secret: "" }, true)).toBe("");
  });

  it("names compose variables like the agent", () => {
    expect(storageVar("backup-disk", "PATH")).toBe("WF_STORAGE_BACKUP_DISK_PATH");
  });
});
