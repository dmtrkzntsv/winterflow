// Storages: a local folder or an S3-compatible bucket registered per server.
// The agent keeps them (and checks everything again); this file holds the
// shapes, the dialog's draft and the syntax rules that give instant
// feedback — mirrors of internal/domain/model/storage.go.

export type StorageType = "local" | "s3";

export type S3Settings = {
  endpoint: string;
  region: string;
  bucket: string;
  prefix: string;
  access_key_id: string;
  secret_access_key: string; // always "<encrypted>" when read
  path_style: boolean;
};

// Storage is a storage as the agent lists it, with the apps using it.
export type Storage = {
  name: string;
  type: StorageType;
  local?: { path: string } | null;
  s3?: S3Settings | null;
  location: string;
  apps: string[] | null; // ids of the apps whose compose files reference it
};

// StorageSaved is storage.save's result.
export type StorageSaved = { storage: Storage; redeployed: string[]; warnings: string[] };

// StorageTest is storage.test's result: a failed probe is ok: false.
export type StorageTest = { ok: boolean; error?: string };

export const STORAGE_TYPES: Record<StorageType, string> = {
  local: "Local folder",
  s3: "S3-compatible",
};

// The sentinel that keeps a stored secret (same as app secrets).
export const ENCRYPTED = "<encrypted>";

// StorageDraft is the dialog's form. secret is the plaintext as typed: ""
// on an edit keeps the stored one.
export type StorageDraft = {
  name: string;
  type: StorageType;
  path: string;
  endpoint: string;
  region: string;
  bucket: string;
  prefix: string;
  accessKeyId: string;
  secret: string;
  pathStyle: boolean;
};

export function draftFrom(s: Storage | null): StorageDraft {
  return {
    name: s?.name ?? "",
    type: s?.type ?? "local",
    path: s?.local?.path ?? "",
    endpoint: s?.s3?.endpoint ?? "",
    region: s?.s3?.region ?? "",
    bucket: s?.s3?.bucket ?? "",
    prefix: s?.s3?.prefix ?? "",
    accessKeyId: s?.s3?.access_key_id ?? "",
    secret: "",
    pathStyle: s?.s3?.path_style ?? false,
  };
}

// storageBody is the API body for a draft; secret is the encrypted secret
// (or ENCRYPTED to keep the stored one).
export function storageBody(d: StorageDraft, secret: string) {
  if (d.type === "local") {
    return { name: d.name.trim(), type: "local" as const, local: { path: d.path.trim() } };
  }
  return {
    name: d.name.trim(),
    type: "s3" as const,
    s3: {
      endpoint: d.endpoint.trim(),
      region: d.region.trim(),
      bucket: d.bucket.trim(),
      prefix: d.prefix.trim(),
      access_key_id: d.accessKeyId.trim(),
      secret_access_key: secret,
      path_style: d.pathStyle,
    },
  };
}

const NAME_RE = /^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$/;
const BUCKET_RE = /^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$/;

// storageNameError mirrors model.ValidateStorageName; "" means valid.
export function storageNameError(name: string): string {
  return NAME_RE.test(name)
    ? ""
    : "Use 1-32 lowercase letters, digits and inner hyphens, like backup-disk.";
}

// privateHost mirrors model.privateHost for IP literals and localhost.
function privateHost(host: string): boolean {
  if (host === "localhost" || host === "[::1]") return true;
  const m = /^(\d+)\.(\d+)\.(\d+)\.(\d+)$/.exec(host);
  if (!m) return false;
  const [a, b] = [Number(m[1]), Number(m[2])];
  return a === 127 || a === 10 || (a === 172 && b >= 16 && b <= 31) || (a === 192 && b === 168) || (a === 169 && b === 254);
}

// endpointError mirrors model.validateEndpoint; "" means valid.
export function endpointError(endpoint: string): string {
  let u: URL;
  try {
    u = new URL(endpoint.trim().replace(/\/+$/, ""));
  } catch {
    return "Use a URL like https://s3.example.com.";
  }
  if (u.pathname !== "/" || u.search || u.hash || u.username) {
    return "Use a URL like https://s3.example.com, without a path.";
  }
  if (u.protocol === "https:") return "";
  if (u.protocol === "http:") {
    return privateHost(u.hostname) ? "" : "Use https: plain http is only allowed for a local or private address.";
  }
  return "Use a URL like https://s3.example.com.";
}

// draftError is the first problem with a draft ("" when it can be sent).
// editing allows an empty secret (keep the stored one).
export function draftError(d: StorageDraft, editing: boolean): string {
  const nameErr = storageNameError(d.name.trim());
  if (nameErr) return nameErr;
  if (d.type === "local") {
    const p = d.path.trim();
    if (!p.startsWith("/")) return "Use an absolute folder path, like /mnt/backup.";
    if (/^\/+$/.test(p)) return "The folder cannot be / itself.";
    return "";
  }
  const endErr = endpointError(d.endpoint);
  if (endErr) return endErr;
  if (!BUCKET_RE.test(d.bucket.trim()) || d.bucket.includes("..")) return "Enter a valid bucket name.";
  const prefix = d.prefix.trim();
  if (prefix.startsWith("/") || prefix.split("/").some((seg, i, all) => (seg === "" && i < all.length - 1) || seg === "." || seg === "..")) {
    return "Use folder names separated by /, like winterflow/.";
  }
  if (!d.accessKeyId.trim() || /\s/.test(d.accessKeyId.trim())) return "Enter the access key.";
  if (!editing && !d.secret) return "Enter the secret key.";
  return "";
}

// storageVar is the compose variable for a storage's field, e.g.
// storageVar("backup-disk", "PATH") = "WF_STORAGE_BACKUP_DISK_PATH".
export function storageVar(name: string, field: string): string {
  return `WF_STORAGE_${name.toUpperCase().replaceAll("-", "_")}_${field}`;
}
