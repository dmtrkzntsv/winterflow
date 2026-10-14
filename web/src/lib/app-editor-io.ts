// Shared load/save plumbing for the app editor, used by both the create-app
// page and the app-details Editor tab: decoding an app.get payload into
// editor state, validating it, and building the app.save payload (encrypting
// touched secrets, passing the "<encrypted>" placeholder through so the agent
// preserves stored values).
import { validHostname } from "@/lib/domains";
import { ENCRYPTED, encryptSecret } from "@/lib/ecies";
import type { FieldErrors } from "@/lib/field-errors";
import type { AppDetailPayload } from "@/context/apps-context-base";
import {
  DEFAULT_COMPOSE,
  isComposeFile,
  localId,
  type AppEditorState,
  type AppSourceConfig,
  type IngressDomain,
  type IngressRedirect,
} from "@/types/app-config";

export function emptyEditorState(): AppEditorState {
  const composeId = localId("file");
  return {
    config: {
      id: "",
      name: "",
      description: "",
      color: "#64748b",
      files: [{ id: composeId, filename: "compose.yml", is_encrypted: false }],
      variables: [],
    },
    files: { [composeId]: DEFAULT_COMPOSE },
    variables: {},
    storageVariables: [],
  };
}

// b64ToString decodes a base64 string to UTF-8 (Go encodes []byte as base64 in
// JSON). Falls back to the raw value if it isn't valid base64.
function b64ToString(b64: string): string {
  try {
    return new TextDecoder().decode(
      Uint8Array.from(atob(b64), (c) => c.charCodeAt(0)),
    );
  } catch {
    return b64;
  }
}

// stateFromDetail builds editor state from an app.get payload. Config metadata
// (which items are secret) comes from the stored config blob; content for files
// and the values for variables come from the payload arrays.
export function stateFromDetail(
  appId: string,
  d: AppDetailPayload,
): AppEditorState {
  const cfgRaw = d.app.config ? b64ToString(d.app.config) : "{}";
  let cfg: {
    name?: string;
    description?: string;
    icon?: string;
    color?: string;
    files?: { filename: string; is_encrypted?: boolean }[];
    variables?: { name: string; is_encrypted?: boolean }[];
  } = {};
  try {
    cfg = JSON.parse(cfgRaw);
  } catch {
    cfg = {};
  }

  const encFile = new Map(
    (cfg.files ?? []).map((f) => [f.filename, !!f.is_encrypted]),
  );
  const encVar = new Map(
    (cfg.variables ?? []).map((v) => [v.name, !!v.is_encrypted]),
  );

  const files: AppEditorState["files"] = {};
  const fileMetas = (d.app.files ?? []).map((f) => {
    const id = localId("file");
    const encrypted = f.encrypted ?? encFile.get(f.name) ?? false;
    // Encrypted content is masked by the agent ("<encrypted>"); keep it as the
    // placeholder so save preserves the stored secret unless re-entered.
    files[id] = encrypted ? ENCRYPTED : b64ToString(f.content);
    return { id, filename: f.name, is_encrypted: encrypted };
  });

  const variables: AppEditorState["variables"] = {};
  const varMetas = (d.app.variables ?? []).map((v) => {
    const id = localId("var");
    const encrypted = v.encrypted ?? encVar.get(v.name) ?? false;
    variables[id] = encrypted ? ENCRYPTED : b64ToString(v.content);
    return { id, name: v.name, is_encrypted: encrypted };
  });

  const cfgSource = (cfg as { source?: import("@/types/app-config").AppSourceConfig }).source;

  const cfgIngress = (cfg as { ingress?: { domains?: unknown[]; redirects?: unknown[] } }).ingress;
  const ingress = cfgIngress
    ? {
        domains: (cfgIngress.domains ?? []).map((dom) => {
          const v = dom as { domain?: string; upstream_port?: number; ssl?: boolean };
          return {
            id: localId("dom"),
            domain: v.domain ?? "",
            upstream_port: v.upstream_port ?? ("" as const),
            ssl: v.ssl ?? false,
          };
        }),
        redirects: (cfgIngress.redirects ?? []).map((r) => {
          const v = r as { domain?: string; path?: string; to?: string; code?: number; ssl?: boolean };
          return {
            id: localId("red"),
            domain: v.domain ?? "",
            path: v.path ?? "",
            to: v.to ?? "",
            code: (v.code ?? 301) as 301 | 302 | 307 | 308,
            ssl: v.ssl ?? false,
          };
        }),
      }
    : undefined;

  return {
    config: {
      id: appId,
      name: cfg.name ?? "",
      description: cfg.description ?? "",
      icon: cfg.icon ?? "",
      color: cfg.color ?? "#64748b",
      files: fileMetas,
      variables: varMetas,
      source: cfgSource,
      ingress,
    },
    files,
    variables,
    storageVariables: d.storage_variables ?? [],
  };
}

// newGitSource is the Git source a newly Git-deployed app starts with.
export function newGitSource(): AppSourceConfig {
  return { repo_url: "", branch: "main", compose_path: "", auto_update: true, poll_seconds: 120 };
}

// newAddressRow is a blank address (hostname -> port) row.
export function newAddressRow(): IngressDomain {
  return { id: localId("dom"), domain: "", upstream_port: "", ssl: true };
}

// pruneBlankAddresses drops the address and redirect rows a create form
// left untouched (its one Address row starts blank), and the ingress itself
// when nothing is left, so an app created without an address saves exactly
// as before (no ingress key). Only the create forms use it: an existing
// app's empty ingress is saved as it was loaded.
export function pruneBlankAddresses(state: AppEditorState): AppEditorState {
  const ing = state.config.ingress;
  if (!ing) return state;
  const domains = ing.domains.filter((d) => d.domain.trim() !== "" || d.upstream_port !== "");
  const redirects = ing.redirects.filter((r) => r.domain.trim() !== "" || r.path.trim() !== "" || r.to.trim() !== "");
  const ingress = domains.length > 0 || redirects.length > 0 ? { domains, redirects } : undefined;
  return { ...state, config: { ...state.config, ingress } };
}

const REDIRECT_CODES = new Set([301, 302, 307, 308]);

// redirectTargetError checks a redirect's target and code. Go's net/url
// leaves Host empty for forms like "http:/foo" or "http:///foo" and
// Validate() rejects them, but WHATWG new URL() normalizes them to a
// non-empty host, so the raw value must also be scheme://non-slash, and
// may not hold whitespace (WHATWG strips embedded tab/newline; Go's
// url.Parse rejects them). The brace check mirrors
// internal/domain/model/ingress.go Validate(): Caddy expands {env.*} and
// {http.request.*} placeholders in the Location header at request time, so
// braces could leak secrets to an attacker-controlled target. The Go check
// is the security boundary; this is faster feedback.
function redirectTargetError(r: IngressRedirect): string {
  const to = r.to.trim();
  let target: URL | null = null;
  try {
    target = new URL(r.to);
  } catch {
    target = null;
  }
  if (
    !target ||
    (target.protocol !== "http:" && target.protocol !== "https:") ||
    target.host === "" ||
    !/^https?:\/\/[^/]/i.test(to) ||
    /\s/.test(to)
  )
    return "Send it to a full address starting with http:// or https://.";
  if (/[{}]/.test(r.to)) return "The target can't contain { or }.";
  if (!REDIRECT_CODES.has(r.code)) return "Pick 301, 302, 307 or 308.";
  return "";
}

// editorErrors names every problem in the editor's state by field: name,
// repo_url, branch, files, file:<id>, var:<id>, domain:<id>, port:<id>,
// redirect-host:<id>, redirect-path:<id>, redirect-to:<id>. Ingress rows are
// read in the order the server's Validate reads them: addresses, then
// redirects, each claiming its hostname; a path rule needs a hostname
// claimed above it.
export function editorErrors(state: AppEditorState): FieldErrors {
  const out: FieldErrors = {};
  const { config } = state;
  if (!config.name.trim()) out.name = "Give the app a name.";
  const src = config.source;
  if (src) {
    if (!src.repo_url.trim()) out.repo_url = "Enter the repository's URL.";
    if (!src.branch.trim()) out.branch = "Enter a branch.";
  }
  // A Git-deployed app may take its compose file from the repository.
  if (!src && !config.files.some((f) => isComposeFile(f.filename))) out.files = "Add a compose file named compose.yml.";
  for (const f of config.files) if (!f.filename.trim()) out[`file:${f.id}`] = "Name this file.";
  for (const v of config.variables) if (!v.name.trim()) out[`var:${v.id}`] = "Name this variable.";

  const ing = config.ingress;
  if (!ing) return out;
  const seen = new Set<string>();
  const claim = (host: string): string => {
    if (!validHostname(host)) return host ? "Use a lowercase hostname like app.example.com." : "Enter a hostname.";
    if (seen.has(host)) return "This hostname is already used above.";
    seen.add(host);
    return "";
  };
  for (const d of ing.domains) {
    const host = claim(d.domain);
    if (host) out[`domain:${d.id}`] = host;
    const port = Number(d.upstream_port);
    if (d.upstream_port === "" || !Number.isInteger(port) || port < 1 || port > 65535)
      out[`port:${d.id}`] = "Enter a port from 1 to 65535.";
  }
  for (const r of ing.redirects) {
    const to = redirectTargetError(r);
    if (to) out[`redirect-to:${r.id}`] = to;
    if (/[{}]/.test(r.path)) out[`redirect-path:${r.id}`] = "The path can't contain { or }.";
    else if (r.path !== "" && !r.path.startsWith("/")) out[`redirect-path:${r.id}`] = "The path must start with /.";
    const host =
      r.path === ""
        ? claim(r.domain)
        : seen.has(r.domain)
          ? ""
          : `Add ${r.domain || "this hostname"} as an address or a whole-domain redirect above this rule first.`;
    if (host) out[`redirect-host:${r.id}`] = host;
  }
  return out;
}

// mapItem encrypts a secret value/file, passes the "<encrypted>" placeholder
// through unchanged (preserve the stored secret), and sends plaintext as-is.
async function mapItem(
  name: string,
  rawContent: string,
  encrypted: boolean,
  publicKey: string,
) {
  let content = rawContent;
  if (encrypted && rawContent !== ENCRYPTED) {
    content = await encryptSecret(rawContent, publicKey);
  }
  return { name: name.trim(), encrypted, content };
}

// buildSavePayload assembles the app.save request body from editor state.
// getPublicKey is only invoked when a secret actually needs encrypting.
export async function buildSavePayload(
  state: AppEditorState,
  getPublicKey: () => Promise<string>,
  appId?: string,
) {
  const needsKey =
    state.config.files.some(
      (f) => f.is_encrypted && (state.files[f.id] ?? "") !== ENCRYPTED,
    ) ||
    state.config.variables.some(
      (v) => v.is_encrypted && (state.variables[v.id] ?? "") !== ENCRYPTED,
    );
  const publicKey = needsKey ? await getPublicKey() : "";

  const files = await Promise.all(
    state.config.files.map((f) =>
      mapItem(f.filename, state.files[f.id] ?? "", f.is_encrypted, publicKey),
    ),
  );
  const variables = await Promise.all(
    state.config.variables.map((v) =>
      mapItem(v.name, state.variables[v.id] ?? "", v.is_encrypted, publicKey),
    ),
  );

  const config = {
    name: state.config.name.trim(),
    description: state.config.description || "",
    icon: state.config.icon || "",
    color: state.config.color || "",
    files: state.config.files.map((f) => ({
      filename: f.filename.trim(),
      is_encrypted: f.is_encrypted,
    })),
    variables: state.config.variables.map((v) => ({
      name: v.name.trim(),
      is_encrypted: v.is_encrypted,
    })),
    ...(state.config.ingress
      ? {
          ingress: {
            domains: state.config.ingress.domains.map((d) => ({
              domain: d.domain.trim(),
              upstream_port: Number(d.upstream_port),
              ssl: d.ssl,
            })),
            redirects: state.config.ingress.redirects.map((r) => ({
              domain: r.domain.trim(),
              ...(r.path ? { path: r.path.trim() } : {}),
              to: r.to.trim(),
              code: r.code,
              ...(r.path ? {} : { ssl: r.ssl }),
            })),
          },
        }
      : {}),
  };

  const app: Record<string, unknown> = {
    name: state.config.name.trim(),
    icon: state.config.icon || "",
    color: state.config.color || "",
  };
  if (appId) app.id = appId;

  // Git source: token is encrypted like any secret; unchanged tokens ride as
  // the placeholder so the agent keeps the stored ciphertext.
  let source: Record<string, unknown> | undefined;
  const src = state.config.source;
  if (src) {
    let token = "";
    if (state.sourceToken) {
      token = await encryptSecret(state.sourceToken, await getPublicKey());
    } else if (src.token_set) {
      token = ENCRYPTED;
    }
    source = {
      repo_url: src.repo_url.trim(),
      branch: src.branch.trim(),
      compose_path: (src.compose_path || "").trim(),
      auto_update: src.auto_update,
      poll_seconds: src.poll_seconds || 0,
      token,
    };
    // The committed config blob carries the source metadata (never the token)
    // so the editor can redisplay it.
    (config as Record<string, unknown>).source = {
      repo_url: source.repo_url,
      branch: source.branch,
      compose_path: source.compose_path,
      auto_update: source.auto_update,
      poll_seconds: source.poll_seconds,
      token_set: Boolean(state.sourceToken) || Boolean(src.token_set),
    };
  }

  return source ? { app, config, files, variables, source } : { app, config, files, variables };
}
