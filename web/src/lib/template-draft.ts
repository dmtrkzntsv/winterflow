// The template editor's working state (TemplateDraft) and its conversions:
// from a stored revision, to the body save-template takes, and from an
// existing app ("Save as template"). A template is its files plus how it
// reads in a list: what an app fills in, which ports it exposes and which
// backups it can turn on are declared in the compose file's x-winterflow
// block, read by the API (inspect-template), never here. The checks give
// instant feedback; the API checks everything again.
import type { AppDetailPayload } from "@/context/apps-context-base";
import { stateFromDetail } from "@/lib/app-editor-io";
import { ENCRYPTED } from "@/lib/ecies";
import { publishedPorts } from "@/lib/host-ports";
import type { TemplateBody } from "@/lib/templates";
import { COMPOSE_FILENAMES, localId, type AppFileMeta } from "@/types/app-config";

// DraftState is what "Save as template" hands the new template's editor:
// the draft, what was left out of it, and the app it was built from.
export type DraftState = { draft: TemplateDraft; notes: string[]; appName: string };

export type TemplateDraft = {
  name: string;
  summary: string;
  description: string;
  icon: string;
  color: string;
  files: AppFileMeta[]; // is_encrypted is always false: a template holds no secret
  contents: Record<string, string>; // file id -> content
};

export const TEMPLATE_NAME = /^[a-z][a-z0-9-]{0,62}$/;
const MAX_SUMMARY = 200;
const MAX_DESCRIPTION = 4000;

// EXAMPLE_COMPOSE is a new template's compose file: one service, its port
// exposed (each app gets a host port of its own) and a variable each app
// fills in.
export const EXAMPLE_COMPOSE = `services:
  app:
    image: traefik/whoami
    environment:
      # The name the page answers with
      WHOAMI_NAME: "whoami"

x-winterflow:
  expose:
    - container: app
      port: 80
  variables:
    WHOAMI_NAME: {}
`;

// emptyDraft is a new template with the example compose file.
export function emptyDraft(): TemplateDraft {
  const id = localId("file");
  return {
    name: "",
    summary: "",
    description: "",
    icon: "",
    color: "#64748b",
    files: [{ id, filename: "compose.yml", is_encrypted: false }],
    contents: { [id]: EXAMPLE_COMPOSE },
  };
}

export function draftFromBody(name: string, body: TemplateBody): TemplateDraft {
  const files: AppFileMeta[] = [];
  const contents: Record<string, string> = {};
  for (const filename of Object.keys(body.files ?? {}).sort()) {
    const id = localId("file");
    files.push({ id, filename, is_encrypted: false });
    contents[id] = body.files![filename];
  }
  return { name, summary: body.summary, description: body.description, icon: body.icon, color: body.color, files, contents };
}

export function bodyFromDraft(d: TemplateDraft): TemplateBody {
  const files: Record<string, string> = {};
  for (const f of d.files) files[f.filename.trim()] = d.contents[f.id] ?? "";
  return { files, summary: d.summary.trim(), description: d.description.trim(), icon: d.icon, color: d.color };
}

// draftProblem is the first thing the API would refuse of the draft's own
// fields, worded for the editor; null when they look saveable. What the
// compose files declare is the Detected panel's (the API reads it).
export function draftProblem(d: TemplateDraft): string | null {
  if (!TEMPLATE_NAME.test(d.name)) {
    return "Name: lowercase letters, digits and hyphens, starting with a letter (at most 63).";
  }
  const summary = d.summary.trim();
  if (summary.length > MAX_SUMMARY || /[\r\n]/.test(summary)) return `Summary: one line of at most ${MAX_SUMMARY} characters.`;
  if (d.description.trim().length > MAX_DESCRIPTION) return `Description: at most ${MAX_DESCRIPTION} characters.`;
  const names = d.files.map((f) => f.filename.trim());
  if (names.some((n) => !n)) return "Every file needs a filename.";
  if (new Set(names).size !== names.length) return "Two files have the same name.";
  if (!names.some((n) => COMPOSE_FILENAMES.includes(n))) {
    return `A compose file is required (${COMPOSE_FILENAMES.join(", ")}).`;
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

// templateFromApp builds revision 1 of a template from an app as the editor
// holds it (Save as template): its files (secret ones left out), name, icon
// and colour. What stays with the app is noted: its variable values,
// hostnames and redirects (each app made from the template has its own), a
// Git source, backups added in the app, and host ports its compose files
// publish themselves (two apps from the template would both claim them).
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
  if (state.config.variables.length > 0) {
    notes.push(
      "The app's variable values stay with the app: declare what each app fills in under x-winterflow.variables.",
    );
  }
  const ports = new Set<number>();
  for (const f of files) {
    if (!COMPOSE_FILENAMES.includes(f.filename.trim())) continue;
    for (const p of publishedPorts(contents[f.id])) ports.add(p.port);
  }
  if (ports.size > 0) {
    const list = [...ports].sort((a, b) => a - b).join(", ");
    notes.push(
      `The compose file publishes host port ${list} itself; two apps from the template would both claim it. List the container port under x-winterflow.expose instead, and each app gets a host port of its own.`,
    );
  }
  if ((state.config.ingress?.domains.length ?? 0) > 0 || (state.config.ingress?.redirects.length ?? 0) > 0) {
    notes.push("Hostnames and redirects stay with the app: each app made from the template picks its own.");
  }
  for (const b of detail.backups ?? []) {
    if (!b.in_recipe) {
      notes.push(
        `The backup ${b.name} was added in the app; add it to the compose file's x-winterflow.backups to make it part of the template.`,
      );
    }
  }
  return {
    draft: {
      name: slugName(state.config.name),
      summary: "",
      description: "",
      icon: state.config.icon ?? "",
      color: state.config.color ?? "#64748b",
      files,
      contents,
    },
    notes,
  };
}
