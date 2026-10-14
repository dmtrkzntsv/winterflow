// The template editor's working state (TemplateDraft) and its conversions:
// from a stored revision, to the body save-template takes, and from an
// existing app ("Save as template"). The checks give instant feedback; the
// API checks everything again (internal/domain/template/validate.go).
import { yamlLanguage } from "@codemirror/lang-yaml";

import type { AppDetailPayload } from "@/context/apps-context-base";
import { stateFromDetail } from "@/lib/app-editor-io";
import { configOf, type EffectiveBackup } from "@/lib/backups";
import { ENCRYPTED } from "@/lib/ecies";
import { publishedPorts, rewriteHostPorts } from "@/lib/host-ports";
import { compilePattern, REF } from "@/lib/template-params";
import type { BackupDefault, Parameter, TemplateBody } from "@/lib/templates";
import { COMPOSE_FILENAMES, defaultCompose, localId, type AppFileMeta } from "@/types/app-config";

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

// DraftState is what "Save as template" hands the new template's editor:
// the draft, what was left out of it, and the app it was built from.
export type DraftState = { draft: TemplateDraft; notes: string[]; appName: string };

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

// Compose's override files in its load order.
const OVERRIDE_FILES = [
  "compose.override.yml",
  "compose.override.yaml",
  "docker-compose.override.yml",
  "docker-compose.override.yaml",
];

export const TEMPLATE_NAME = /^[a-z][a-z0-9-]{0,62}$/;
const PARAM_NAME = /^[A-Z][A-Z0-9_]*$/;
const PORT = /^[1-9][0-9]{0,4}$/;

// emptyDraft is a new template: the example compose file publishing
// ${PORT}, with the PORT parameter (default 8080) each app sets to a port of
// its own.
export function emptyDraft(): TemplateDraft {
  const id = localId("file");
  return {
    name: "",
    appName: "",
    icon: "",
    color: "#64748b",
    files: [{ id, filename: "compose.yml", is_encrypted: false }],
    contents: { [id]: defaultCompose("${PORT}") },
    parameters: [paramRow({ name: "PORT", default: "8080", description: "Host port" })],
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
// first main file and the first override (cheap: no parsing).
export function composeContents(d: TemplateDraft): string[] {
  const byName = new Map(d.files.map((f) => [f.filename.trim(), d.contents[f.id] ?? ""]));
  const main = COMPOSE_FILENAMES.find((n) => byName.has(n));
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
  // A leading "---" is a DirectiveEnd child, so look the mapping up by name.
  const root = doc?.getChild("BlockMapping") ?? doc?.getChild("FlowMapping");
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

// backupNamesIn are the backup names compose contents declare, in order,
// without duplicates (undefined: no such file).
export function backupNamesIn(contents: (string | undefined)[]): string[] {
  return [...new Set(contents.flatMap((src) => (src === undefined ? [] : recipeNamesIn(src))))];
}

// recipeBackupNames are the backup names the draft's compose files declare
// (the Backup defaults rows).
export function recipeBackupNames(d: TemplateDraft): string[] {
  return backupNamesIn(composeContents(d));
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

const utf8 = new TextEncoder();

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
  if (!names.some((n) => COMPOSE_FILENAMES.includes(n))) {
    return `A compose file is required (${COMPOSE_FILENAMES.join(", ")}).`;
  }
  const seen = new Set<string>();
  const plain = new Set<string>();
  for (const p of d.parameters) {
    const name = p.name.trim();
    if (!PARAM_NAME.test(name)) return `Parameter "${name}": uppercase letters, digits and _, starting with a letter.`;
    if (name === "COMPOSE_PROJECT_NAME" || name.startsWith("WF_")) return `Parameter ${name} is reserved.`;
    if (seen.has(name)) return `Parameter ${name} is declared twice.`;
    seen.add(name);
    if (utf8.encode(p.pattern).length > 512) return `Parameter ${name}: the pattern is longer than 512 bytes.`;
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

// slugName makes a template name from an app name.
export function slugName(appName: string): string {
  const slug = appName
    .toLowerCase()
    .replace(/[^a-z0-9-]+/g, "-")
    .replace(/^[^a-z]+/, "")
    .slice(0, 63)
    .replace(/-+$/, "");
  return slug || "template";
}

// isComposeYaml: a file whose ports templateFromApp turns into parameters.
const isComposeYaml = (filename: string) => /\.ya?ml$/i.test(filename.trim());

// freeParamName is base, else base_2, base_3, … whichever no parameter has.
function freeParamName(base: string, taken: Set<string>): string {
  let name = base;
  for (let n = 2; taken.has(name); n++) name = `${base}_${n}`;
  taken.add(name);
  return name;
}

// recipeEntry is the x-winterflow.backups entry of a backup added in the
// app (what and where it copies; on/off, storage and schedule are the
// template's defaults).
function recipeEntry(b: EffectiveBackup): Record<string, unknown> {
  const c = configOf(b);
  const entry: Record<string, unknown> = { name: b.name, type: b.type };
  for (const k of ["volume", "service", "path", "paths", "exclude"] as const) if (c[k] !== undefined) entry[k] = c[k];
  return entry;
}

// templateFromApp builds revision 1 of a template from an app as the editor
// holds it (Save as template): files copied, each variable a parameter with
// its value as the default, each secret a required secret parameter, every
// literal host port the compose files publish a PORT parameter (PORT_2, …)
// defaulting to it, the app's hosts literal ingress rows (on that parameter
// where they point at the port), its backups the defaults — one added in
// the app joins the compose file's x-winterflow.backups so the template
// declares it. notes say what changed or could not come along.
export function templateFromApp(appId: string, detail: AppDetailPayload): { draft: TemplateDraft; notes: string[] } {
  const state = stateFromDetail(appId, detail);
  const notes: string[] = [];
  if (state.config.source) {
    notes.push("The app deploys from a Git repository; a template holds files only, so the repository is not part of it.");
  }
  const files: AppFileMeta[] = [];
  const contents: Record<string, string> = {};
  for (const f of state.config.files) {
    if (f.is_encrypted || state.files[f.id] === ENCRYPTED) {
      notes.push(`${f.filename} is a secret file; a template holds no secrets, so it was left out.`);
      continue;
    }
    files.push({ ...f, is_encrypted: false });
    contents[f.id] = state.files[f.id] ?? "";
  }
  const parameters = state.config.variables.map((v) => {
    if (v.is_encrypted) return paramRow({ name: v.name, secret: true, required: true });
    const value = state.variables[v.id] ?? "";
    if (value.includes("$")) {
      notes.push(`${v.name}'s value has a $, which a parameter can't hold; it has no default now.`);
      return paramRow({ name: v.name });
    }
    return paramRow({ name: v.name, default: value });
  });

  // Each app from the template needs host ports of its own: a literal one
  // becomes a parameter defaulting to it.
  const taken = new Set(parameters.map((p) => p.name));
  const portParams = new Map<number, string>();
  for (const f of files) {
    if (!isComposeYaml(f.filename)) continue;
    for (const { port } of publishedPorts(contents[f.id])) {
      if (portParams.has(port)) continue;
      const name = freeParamName("PORT", taken);
      portParams.set(port, name);
      parameters.push(paramRow({ name, required: true, default: String(port), description: "Host port" }));
      notes.push(`Port ${port} is now the ${name} parameter, so each app can publish its own.`);
    }
  }
  if (portParams.size > 0) {
    for (const f of files) {
      if (!isComposeYaml(f.filename)) continue;
      contents[f.id] = rewriteHostPorts(contents[f.id], (port) => {
        const name = portParams.get(port);
        return name ? `\${${name}}` : null;
      });
    }
  }
  const ingress = (state.config.ingress?.domains ?? []).map((dom) => {
    const name = dom.upstream_port === "" ? undefined : portParams.get(dom.upstream_port);
    return ingressRow(dom.domain, name ? `\${${name}}` : String(dom.upstream_port));
  });
  if ((state.config.ingress?.redirects.length ?? 0) > 0) notes.push("Redirects aren't part of templates; they were left out.");

  const backups: Record<string, BackupDefault> = {};
  const added: EffectiveBackup[] = [];
  for (const b of detail.backups ?? []) {
    if (!b.in_recipe) added.push(b);
    const c = configOf(b);
    backups[b.name] = {
      enabled: !!c.enabled,
      ...(c.storage ? { storage: c.storage } : {}),
      ...(c.every ? { every: c.every } : {}),
      ...(c.keep ? { keep: c.keep } : {}),
    };
  }
  if (added.length > 0) {
    const main = files.find((f) => COMPOSE_FILENAMES.includes(f.filename.trim()));
    const src = main ? contents[main.id] : "";
    if (main && !/^x-winterflow\s*:/m.test(src)) {
      const entries = added.map((b) => `    - ${JSON.stringify(recipeEntry(b))}`).join("\n");
      contents[main.id] = `${src.replace(/\s*$/, "")}\n\nx-winterflow:\n  backups:\n${entries}\n`;
    } else {
      for (const b of added) {
        delete backups[b.name];
        notes.push(
          `The backup ${b.name} was added in the app; add it to the compose file's x-winterflow.backups to make it part of the template.`,
        );
      }
    }
  }
  return {
    draft: {
      name: slugName(state.config.name),
      appName: state.config.name,
      icon: state.config.icon ?? "",
      color: state.config.color ?? "#64748b",
      files,
      contents,
      parameters,
      ingress,
      backups,
    },
    notes,
  };
}
