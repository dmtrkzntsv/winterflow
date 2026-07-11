// Shared load/save plumbing for the app editor, used by both the create-app
// page and the app-details Editor tab: decoding an app.get payload into
// editor state, validating it, and building the app.save payload (encrypting
// touched secrets, passing the "<encrypted>" placeholder through so the agent
// preserves stored values).
import { encryptSecret } from "@/lib/ecies";
import type { AppDetailPayload } from "@/context/apps-context-base";
import {
  DEFAULT_COMPOSE,
  localId,
  type AppEditorState,
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
    files[id] = encrypted ? "<encrypted>" : b64ToString(f.content);
    return { id, filename: f.name, is_encrypted: encrypted };
  });

  const variables: AppEditorState["variables"] = {};
  const varMetas = (d.app.variables ?? []).map((v) => {
    const id = localId("var");
    const encrypted = v.encrypted ?? encVar.get(v.name) ?? false;
    variables[id] = encrypted ? "<encrypted>" : b64ToString(v.content);
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
  };
}

export function validateEditorState(state: AppEditorState): string | null {
  if (!state.config.name.trim()) return "App name is required.";
  const src = state.config.source;
  if (src) {
    if (!src.repo_url.trim()) return "Repository URL is required.";
    if (!src.branch.trim()) return "Branch is required.";
  }
  const hasCompose = state.config.files.some(
    (f) =>
      f.filename.trim() === "compose.yml" ||
      f.filename.trim() === "docker-compose.yml",
  );
  // Git-sourced apps may take their compose file from the repo.
  if (!hasCompose && !src)
    return "A compose.yml (or docker-compose.yml) file is required.";
  for (const f of state.config.files) {
    if (!f.filename.trim()) return "Every file needs a filename.";
  }
  for (const v of state.config.variables) {
    if (!v.name.trim()) return "Every variable needs a name.";
  }
  // Mirrors Go's Ingress.Validate() (internal/domain/model/ingress.go): strict
  // lowercase RFC-1123 hostname, capped at 253 chars total.
  const hostnameRe = /^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*$/;
  const validHostname = (h: string) => h.length <= 253 && hostnameRe.test(h);
  const validRedirectCodes = new Set([301, 302, 307, 308]);
  const ing = state.config.ingress;
  if (ing) {
    const seen = new Set<string>();
    for (const d of ing.domains) {
      if (!validHostname(d.domain)) return `"${d.domain}" is not a valid lowercase hostname.`;
      if (seen.has(d.domain)) return `Duplicate domain "${d.domain}".`;
      seen.add(d.domain);
      const port = Number(d.upstream_port);
      if (!Number.isInteger(port) || port < 1 || port > 65535)
        return `Domain "${d.domain}": upstream port must be 1-65535.`;
    }
    for (const r of ing.redirects) {
      let target: URL;
      try {
        target = new URL(r.to);
      } catch {
        return `Redirect for "${r.domain}": target must be an absolute http(s) URL.`;
      }
      // Go's net/url leaves Host empty for forms like "http:/foo" or
      // "http:///foo" and Validate() rejects them, but WHATWG new URL()
      // normalizes them to a non-empty host — so also require the raw value
      // to be scheme://non-slash to match what the server will accept.
      if (
        (target.protocol !== "http:" && target.protocol !== "https:") ||
        target.host === "" ||
        !/^https?:\/\/[^/]/i.test(r.to.trim()) ||
        // WHATWG strips embedded tab/newline; Go's url.Parse rejects them.
        /\s/.test(r.to.trim())
      )
        return `Redirect for "${r.domain}": target must be an absolute http(s) URL.`;
      if (!validRedirectCodes.has(r.code))
        return `Redirect for "${r.domain}": code must be one of 301, 302, 307, 308.`;
      if (r.path === "") {
        if (!validHostname(r.domain)) return `"${r.domain}" is not a valid lowercase hostname.`;
        if (seen.has(r.domain)) return `Duplicate domain "${r.domain}".`;
        seen.add(r.domain);
      } else {
        if (!r.path.startsWith("/")) return `Redirect path "${r.path}" must start with /.`;
        if (!seen.has(r.domain))
          return `Path rule for "${r.domain}": add that domain as a route or redirect first.`;
      }
    }
  }
  return null;
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
  if (encrypted && rawContent !== "<encrypted>") {
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
      (f) => f.is_encrypted && (state.files[f.id] ?? "") !== "<encrypted>",
    ) ||
    state.config.variables.some(
      (v) => v.is_encrypted && (state.variables[v.id] ?? "") !== "<encrypted>",
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
      token = "<encrypted>";
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
