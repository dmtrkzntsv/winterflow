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

// Length caps in UTF-8 bytes (Go's len), as model.storage.go: they keep a
// storage's location inside the API index's VARCHAR(1024).
const MAX_PATH_BYTES = 512;
const MAX_ENDPOINT_BYTES = 255;
const MAX_PREFIX_BYTES = 512;

const byteLength = (s: string) => new TextEncoder().encode(s).length;

// hasControl mirrors model.hasControl: a C0 control character or DEL.
// eslint-disable-next-line no-control-regex
const hasControl = (s: string) => /[\u0000-\u001f\u007f]/.test(s);

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
  const normalized = endpoint.trim().replace(/\/+$/, "");
  if (byteLength(normalized) > MAX_ENDPOINT_BYTES) return `The endpoint can be at most ${MAX_ENDPOINT_BYTES} bytes long.`;
  let u: URL;
  try {
    u = new URL(normalized);
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

// DraftField is a field of the dialog a problem belongs to.
export type DraftField = "name" | "path" | "endpoint" | "bucket" | "prefix" | "accessKeyId" | "secret";

// DraftProblem is the first problem with a draft and the field it belongs to.
export type DraftProblem = { field: DraftField; message: string };

// draftProblem is the first problem with a draft (null when it can be sent).
// editing allows an empty secret (keep the stored one); existing are the
// server's storage names, which an add can't reuse.
export function draftProblem(d: StorageDraft, editing: boolean, existing: readonly string[] = []): DraftProblem | null {
  const at = (field: DraftField, message: string): DraftProblem => ({ field, message });
  const name = d.name.trim();
  const nameErr = storageNameError(name);
  if (nameErr) return at("name", nameErr);
  if (!editing && existing.includes(name)) return at("name", `A storage named ${name} already exists. Edit it instead.`);
  if (d.type === "local") {
    const p = d.path.trim();
    if (!p.startsWith("/")) return at("path", "Use an absolute folder path, like /mnt/backup/winterflow.");
    if (hasControl(p)) return at("path", "The folder path cannot contain control characters (newline, tab, …).");
    if (/^\/+$/.test(p)) return at("path", "The folder cannot be / itself.");
    if (byteLength(p) > MAX_PATH_BYTES) return at("path", `The folder path can be at most ${MAX_PATH_BYTES} bytes long.`);
    return null;
  }
  const endErr = endpointError(d.endpoint);
  if (endErr) return at("endpoint", endErr);
  if (!BUCKET_RE.test(d.bucket.trim()) || d.bucket.includes("..")) return at("bucket", "Enter a valid bucket name.");
  const prefix = d.prefix.trim();
  // The agent stores the prefix ending in "/": measure that form.
  const storedPrefix = prefix && !prefix.endsWith("/") ? `${prefix}/` : prefix;
  if (byteLength(storedPrefix) > MAX_PREFIX_BYTES || ["\\", "\u0000", "\r", "\n", "\t"].some((c) => prefix.includes(c)) || prefix.startsWith("/") || prefix.split("/").some((seg, i, all) => (seg === "" && i < all.length - 1) || seg === "." || seg === "..")) {
    return at("prefix", "Use folder names separated by /, like winterflow/.");
  }
  const keyId = d.accessKeyId.trim();
  if (hasControl(keyId)) return at("accessKeyId", "The access key cannot contain control characters (newline, tab, …).");
  if (!keyId || keyId.includes(" ") || byteLength(keyId) > 128) return at("accessKeyId", "Enter the access key.");
  if (!editing && !d.secret) return at("secret", "Enter the secret key.");
  return null;
}

// draftError is draftProblem's message ("" when the draft can be sent).
export function draftError(d: StorageDraft, editing: boolean, existing: readonly string[] = []): string {
  return draftProblem(d, editing, existing)?.message ?? "";
}

// pathStyleDefault says whether an endpoint needs path-style addressing: an
// IP address or localhost has no wildcard DNS for bucket.host names (MinIO,
// Garage and other self-hosted stores on the LAN).
export function pathStyleDefault(endpoint: string): boolean {
  let host: string;
  try {
    host = new URL(endpoint.trim()).hostname;
  } catch {
    return false;
  }
  return host === "localhost" || host.startsWith("[") || /^\d+\.\d+\.\d+\.\d+$/.test(host);
}

// storageVar is the compose variable for a storage's field, e.g.
// storageVar("backup-disk", "PATH") = "WF_STORAGE_BACKUP_DISK_PATH".
export function storageVar(name: string, field: string): string {
  return `WF_STORAGE_${name.toUpperCase().replaceAll("-", "_")}_${field}`;
}
