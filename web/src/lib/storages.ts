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
  secret_access_key: string; // always ENCRYPTED (lib/ecies) when read
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
  const name = d.name.trim();
  if (d.type === "local") {
    return { name, type: "local" as const, local: { path: d.path.trim() } };
  }
  return {
    name,
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

// endpointError mirrors model.validateEndpoint's syntax; "" means valid.
// Whether plain http is allowed (a loopback or private address only) is
// the agent's call: its answer shows next to the form.
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
  return u.protocol === "https:" || u.protocol === "http:" ? "" : "Use a URL like https://s3.example.com.";
}

// prefixValid mirrors model.validatePrefix: "" or folder names separated by
// "/", measured as the agent stores it (ending in "/").
function prefixValid(prefix: string): boolean {
  if (!prefix) return true;
  const stored = prefix.endsWith("/") ? prefix : `${prefix}/`;
  return (
    byteLength(stored) <= MAX_PREFIX_BYTES &&
    !["\\", "\u0000", "\r", "\n", "\t"].some((c) => prefix.includes(c)) &&
    !prefix.startsWith("/") &&
    stored.split("/").every((seg, i, all) => (seg !== "" || i === all.length - 1) && seg !== "." && seg !== "..")
  );
}

function pathError(p: string): string {
  if (!p.startsWith("/")) return "Use an absolute folder path, like /mnt/backup/winterflow.";
  if (hasControl(p)) return "The folder path cannot contain control characters (newline, tab, …).";
  if (/^\/+$/.test(p)) return "The folder cannot be / itself.";
  if (byteLength(p) > MAX_PATH_BYTES) return `The folder path can be at most ${MAX_PATH_BYTES} bytes long.`;
  return "";
}

function accessKeyError(keyId: string): string {
  if (hasControl(keyId)) return "The access key cannot contain control characters (newline, tab, …).";
  if (!keyId || keyId.includes(" ") || byteLength(keyId) > 128) return "Enter the access key.";
  return "";
}

// DraftField is a field of the dialog, in form order; a problem belongs to
// one.
export const DRAFT_FIELDS = ["name", "path", "endpoint", "region", "bucket", "prefix", "accessKeyId", "secret"] as const;
export type DraftField = (typeof DRAFT_FIELDS)[number];

// DraftProblem is the first problem with a draft and the field it belongs to.
export type DraftProblem = { field: DraftField; message: string };

// RULES are each type's checks after the name, in form order: a check
// answers its problem, "" when the draft passes it.
const RULES: Record<StorageType, [DraftField, (d: StorageDraft, editing: boolean) => string][]> = {
  local: [["path", (d) => pathError(d.path.trim())]],
  s3: [
    ["endpoint", (d) => endpointError(d.endpoint)],
    ["bucket", (d) => (BUCKET_RE.test(d.bucket.trim()) && !d.bucket.includes("..") ? "" : "Enter a valid bucket name.")],
    ["prefix", (d) => (prefixValid(d.prefix.trim()) ? "" : "Use folder names separated by /, like winterflow/.")],
    ["accessKeyId", (d) => accessKeyError(d.accessKeyId.trim())],
    // An edit keeps the stored secret when none is typed.
    ["secret", (d, editing) => (editing || d.secret ? "" : "Enter the secret key.")],
  ],
};

// draftProblem is the first problem with a draft (null when it can be sent).
// The name mirrors model.ValidateStorageName; existing are the server's
// storage names, which an add can't reuse.
export function draftProblem(d: StorageDraft, editing: boolean, existing: readonly string[] = []): DraftProblem | null {
  const name = d.name.trim();
  if (!NAME_RE.test(name)) return { field: "name", message: "Use 1-32 lowercase letters, digits and inner hyphens, like backup-disk." };
  if (!editing && existing.includes(name)) return { field: "name", message: `A storage named ${name} already exists. Edit it instead.` };
  for (const [field, check] of RULES[d.type]) {
    const message = check(d, editing);
    if (message) return { field, message };
  }
  return null;
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
