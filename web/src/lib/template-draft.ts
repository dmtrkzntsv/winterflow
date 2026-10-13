// The template editor's working state (TemplateDraft) and its conversions:
// from a stored revision and to the body save-template takes. The checks
// give instant feedback; the API checks everything again
// (internal/domain/template/validate.go).
import { yamlLanguage } from "@codemirror/lang-yaml";

import { compilePattern } from "@/lib/template-params";
import type { BackupDefault, Parameter, TemplateBody } from "@/lib/templates";
import { DEFAULT_COMPOSE, localId, type AppFileMeta } from "@/types/app-config";

export type ParamRow = {
  id: string;
  name: string;
  secret: boolean;
  required: boolean;
  default: string; // "" = none (renders the same as an empty default)
  pattern: string;
  description: string;
};

export type IngressDraft = { id: string; host: string; service: string; port: string };

export type TemplateDraft = {
  name: string;
  appName: string;
  icon: string;
  color: string;
  files: AppFileMeta[]; // is_encrypted is always false: a template holds no secret
  contents: Record<string, string>; // file id -> content
  parameters: ParamRow[];
  ingress: IngressDraft[];
  backups: Record<string, BackupDefault>; // by recipe backup name
};

// Compose files in compose's load order (model.ComposeFileNames and the
// overrides).
const COMPOSE_FILES = ["compose.yaml", "compose.yml", "docker-compose.yml", "docker-compose.yaml"];
const OVERRIDE_FILES = [
  "compose.override.yml",
  "compose.override.yaml",
  "docker-compose.override.yml",
  "docker-compose.override.yaml",
];

export const TEMPLATE_NAME = /^[a-z][a-z0-9-]{0,62}$/;
const PARAM_NAME = /^[A-Z][A-Z0-9_]*$/;
const PORT = /^[1-9][0-9]{0,4}$/;
const REF = /\$\{([^}]*)\}/g;

export function emptyDraft(): TemplateDraft {
  const id = localId("file");
  return {
    name: "",
    appName: "",
    icon: "",
    color: "#64748b",
    files: [{ id, filename: "compose.yml", is_encrypted: false }],
    contents: { [id]: DEFAULT_COMPOSE },
    parameters: [],
    ingress: [],
    backups: {},
  };
}

export function paramRow(p: Partial<Parameter> = {}): ParamRow {
  return {
    id: localId("param"),
    name: p.name ?? "",
    secret: !!p.secret,
    required: !!p.required,
    default: p.default ?? "",
    pattern: p.pattern ?? "",
    description: p.description ?? "",
  };
}

export function ingressRow(host = "", port = "", service = ""): IngressDraft {
  return { id: localId("ing"), host, service, port };
}

export function draftFromBody(name: string, body: TemplateBody): TemplateDraft {
  const files: AppFileMeta[] = [];
  const contents: Record<string, string> = {};
  for (const filename of Object.keys(body.files ?? {}).sort()) {
    const id = localId("file");
    files.push({ id, filename, is_encrypted: false });
    contents[id] = body.files![filename];
  }
  return {
    name,
    appName: body.app_name,
    icon: body.icon,
    color: body.color,
    files,
    contents,
    parameters: (body.parameters ?? []).map(paramRow),
    ingress: (body.ingress ?? []).map((r) => ingressRow(r.host, String(r.port), r.service ?? "")),
    backups: { ...(body.backups ?? {}) },
  };
}

// composeContents are the draft's compose files compose would load: the
// first main file and the first override.
function composeContents(d: TemplateDraft): string[] {
  const byName = new Map(d.files.map((f) => [f.filename.trim(), d.contents[f.id] ?? ""]));
  const main = COMPOSE_FILES.find((n) => byName.has(n));
  if (!main) return [];
  const override = OVERRIDE_FILES.find((n) => byName.has(n));
  return override ? [byName.get(main)!, byName.get(override)!] : [byName.get(main)!];
}

type YamlNode = ReturnType<typeof yamlLanguage.parser.parse>["topNode"];

function scalar(src: string, n: YamlNode | null | undefined): string {
  if (!n) return "";
  const text = src.slice(n.from, n.to).trim();
  return n.name === "QuotedLiteral" ? text.slice(1, -1) : text;
}

// entries are a mapping node's key/value pairs.
function entries(src: string, n: YamlNode | null | undefined): [string, YamlNode | null][] {
  if (!n || (n.name !== "BlockMapping" && n.name !== "FlowMapping")) return [];
  const out: [string, YamlNode | null][] = [];
  for (let pair = n.firstChild; pair; pair = pair.nextSibling) {
    if (pair.name !== "Pair") continue;
    const key = pair.getChild("Key");
    let value = key?.nextSibling ?? null;
    while (value && (value.name === ":" || value.name === "Comment")) value = value.nextSibling;
    out.push([scalar(src, key?.firstChild), value]);
  }
  return out;
}

// recipeNamesIn reads the backup names one compose file declares under
// x-winterflow.backups (a list of mappings with a name).
function recipeNamesIn(src: string): string[] {
  const doc = yamlLanguage.parser.parse(src).topNode.getChild("Document");
  const root = doc?.firstChild;
  const xwf = entries(src, root).find(([k]) => k === "x-winterflow")?.[1];
  const list = entries(src, xwf).find(([k]) => k === "backups")?.[1];
  if (!list || (list.name !== "BlockSequence" && list.name !== "FlowSequence")) return [];
  const names: string[] = [];
  for (const item of list.getChildren("Item")) {
    const name = entries(src, item.firstChild).find(([k]) => k === "name")?.[1];
    const text = scalar(src, name);
    if (text) names.push(text);
  }
  return names;
}

// recipeBackupNames are the backup names the draft's compose files declare
// (the Backup defaults rows), in order, without duplicates.
export function recipeBackupNames(d: TemplateDraft): string[] {
  return [...new Set(composeContents(d).flatMap(recipeNamesIn))];
}

export function bodyFromDraft(d: TemplateDraft): TemplateBody {
  const files: Record<string, string> = {};
  for (const f of d.files) files[f.filename.trim()] = d.contents[f.id] ?? "";
  const recipe = new Set(recipeBackupNames(d));
  const backups: Record<string, BackupDefault> = {};
  for (const [name, def] of Object.entries(d.backups)) if (recipe.has(name)) backups[name] = def;
  return {
    files,
    parameters: d.parameters.map((p) => {
      const out: Parameter = { name: p.name.trim() };
      if (p.secret) out.secret = true;
      if (p.required) out.required = true;
      if (!p.secret && p.default !== "") out.default = p.default;
      if (p.pattern) out.pattern = p.pattern;
      if (p.description.trim()) out.description = p.description.trim();
      return out;
    }),
    app_name: d.appName.trim(),
    icon: d.icon,
    color: d.color,
    ingress: d.ingress.map((r) => {
      const port = r.port.trim();
      return {
        host: r.host.trim(),
        ...(r.service.trim() ? { service: r.service.trim() } : {}),
        port: PORT.test(port) ? Number(port) : port,
      };
    }),
    backups,
  };
}

// refProblem names the first ${...} in s that is not a plain parameter.
function refProblem(field: string, s: string, plain: Set<string>): string | null {
  for (const m of s.matchAll(REF)) {
    if (!plain.has(m[1])) return `${field}: \${${m[1]}} is not a plain parameter of the template.`;
  }
  return null;
}

// draftProblem is the first thing the API would refuse, worded for the
// editor; null when the draft looks saveable.
export function draftProblem(d: TemplateDraft): string | null {
  if (!TEMPLATE_NAME.test(d.name)) {
    return "Name: lowercase letters, digits and hyphens, starting with a letter (at most 63).";
  }
  if (!d.appName.trim()) return "App name is required.";
  const names = d.files.map((f) => f.filename.trim());
  if (names.some((n) => !n)) return "Every file needs a filename.";
  if (new Set(names).size !== names.length) return "Two files have the same name.";
  if (!names.some((n) => COMPOSE_FILES.includes(n))) return `A compose file is required (${COMPOSE_FILES.join(", ")}).`;
  const seen = new Set<string>();
  const plain = new Set<string>();
  for (const p of d.parameters) {
    const name = p.name.trim();
    if (!PARAM_NAME.test(name)) return `Parameter "${name}": uppercase letters, digits and _, starting with a letter.`;
    if (name === "COMPOSE_PROJECT_NAME" || name.startsWith("WF_")) return `Parameter ${name} is reserved.`;
    if (seen.has(name)) return `Parameter ${name} is declared twice.`;
    seen.add(name);
    if (new TextEncoder().encode(p.pattern).length > 512) return `Parameter ${name}: the pattern is longer than 512 bytes.`;
    if (p.secret) continue;
    plain.add(name);
    if (p.default.includes("$")) return `Parameter ${name}: the default may not contain $.`;
    const re = compilePattern(p.pattern);
    if (p.default !== "" && re && !re.test(p.default)) return `Parameter ${name}: the default doesn't match the pattern.`;
  }
  const app = refProblem("App name", d.appName, plain);
  if (app) return app;
  for (const r of d.ingress) {
    if (!r.host.trim()) return "Every ingress row needs a host.";
    const host = refProblem(`Host ${r.host}`, r.host, plain);
    if (host) return host;
    const port = r.port.trim();
    const ref = /^\$\{([^}]*)\}$/.exec(port);
    if (ref ? !plain.has(ref[1]) : !PORT.test(port) || Number(port) > 65535) {
      return `Host ${r.host}: the port is a number 1-65535 or \${PARAM} of a plain parameter.`;
    }
  }
  return null;
}
