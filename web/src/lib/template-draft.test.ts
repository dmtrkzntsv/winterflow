import { describe, expect, it } from "vitest";

import type { AppDetailPayload } from "@/context/apps-context-base";
import type { EffectiveBackup } from "@/lib/backups";
import {
  bodyFromDraft,
  draftFromBody,
  draftProblem,
  emptyDraft,
  EXAMPLE_COMPOSE,
  slugName,
  templateFromApp,
  type TemplateDraft,
} from "@/lib/template-draft";
import type { TemplateBody } from "@/lib/templates";

const COMPOSE = `services:
  app:
    image: ghcr.io/example/notes:1
    environment:
      BASE_URL: "http://localhost:8080"
x-winterflow:
  expose:
    - { container: app, port: 8080 }
  variables:
    BASE_URL: { required: true }
`;

const BODY: TemplateBody = {
  files: { "compose.yaml": COMPOSE, Caddyfile: ":80" },
  summary: "Notes for teams",
  description: "A longer text.",
  icon: "note",
  color: "#2563eb",
};

function valid(): TemplateDraft {
  return draftFromBody("notes", BODY);
}

describe("draftFromBody / bodyFromDraft", () => {
  it("round-trips a body", () => {
    expect(bodyFromDraft(valid())).toEqual(BODY);
  });

  it("trims the summary and description, and reads null files from the API", () => {
    const d = { ...valid(), summary: "  Notes  ", description: " text \n" };
    expect(bodyFromDraft(d)).toMatchObject({ summary: "Notes", description: "text" });
    expect(draftFromBody("x", { ...BODY, files: null }).files).toEqual([]);
  });
});

describe("draftProblem", () => {
  const cases: [string, (d: TemplateDraft) => TemplateDraft, string][] = [
    ["a bad name", (d) => ({ ...d, name: "Notes" }), "Name:"],
    ["a two-line summary", (d) => ({ ...d, summary: "a\nb" }), "Summary:"],
    ["a long summary", (d) => ({ ...d, summary: "s".repeat(201) }), "Summary:"],
    ["a long description", (d) => ({ ...d, description: "d".repeat(4001) }), "Description:"],
    ["a file without a name", (d) => ({ ...d, files: [...d.files, { id: "x", filename: " ", is_encrypted: false }] }), "Every file needs a filename."],
    ["two files of one name", (d) => ({ ...d, files: [...d.files, { ...d.files[0], id: "y" }] }), "Two files have the same name."],
    ["no compose file", (d) => ({ ...d, files: d.files.filter((f) => f.filename !== "compose.yaml") }), "A compose file is required"],
  ];
  for (const [name, edit, want] of cases) {
    it(`refuses ${name}`, () => {
      expect(draftProblem(edit(valid()))).toContain(want);
    });
  }

  it("accepts a valid draft", () => {
    expect(draftProblem(valid())).toBeNull();
  });
});

describe("a new template", () => {
  it("starts from the example compose file, its port exposed and a variable declared", () => {
    const body = bodyFromDraft({ ...emptyDraft(), name: "site" });
    expect(body.files).toEqual({ "compose.yml": EXAMPLE_COMPOSE });
    expect(EXAMPLE_COMPOSE).toContain("x-winterflow:");
    expect(draftProblem({ ...emptyDraft(), name: "site" })).toBeNull();
  });
});

describe("templateFromApp", () => {
  const b64 = (s: string) => btoa(s);
  const compose = `services:
  web:
    image: nginx
    ports:
      - "8090:80"
`;
  const config = {
    name: "Shop Front",
    icon: "cart",
    color: "#111111",
    files: [{ filename: "compose.yml" }, { filename: "key.pem", is_encrypted: true }, { filename: "notes.txt" }],
    variables: [{ name: "NAME" }],
    ingress: {
      domains: [{ domain: "shop.example.com", upstream_port: 8090 }],
      redirects: [],
    },
  };
  const backup = (b: Partial<EffectiveBackup>): EffectiveBackup =>
    ({ name: "db", type: "sqlite", enabled: true, storage: "tenants", in_recipe: true, from_recipe: { every: true }, ...b }) as EffectiveBackup;
  const detail: AppDetailPayload = {
    app: {
      app_id: "a1",
      config: b64(JSON.stringify(config)),
      files: [
        { name: "compose.yml", content: b64(compose) },
        { name: "key.pem", content: "<encrypted>", encrypted: true },
        { name: "notes.txt", content: b64("8090:80 stays") },
      ],
      variables: [{ name: "NAME", content: b64("shop") }],
    },
    backups: [backup({}), backup({ name: "extra", in_recipe: false })],
    revision: 1,
    available_revisions: null,
  };

  it("copies the files and the look, and notes what stays with the app", () => {
    const { draft, notes } = templateFromApp("a1", detail);
    expect(draft.name).toBe("shop-front");
    expect(bodyFromDraft(draft)).toEqual({
      files: { "compose.yml": compose, "notes.txt": "8090:80 stays" },
      summary: "",
      description: "",
      icon: "cart",
      color: "#111111",
    });
    expect(notes).toEqual([
      "key.pem is a secret file; a template holds no secrets, so it was left out.",
      "The app's variable values stay with the app: declare what each app fills in under x-winterflow.variables.",
      "The compose file publishes host port 8090 itself; two apps from the template would both claim it. List the container port under x-winterflow.expose instead, and each app gets a host port of its own.",
      "Hostnames and redirects stay with the app: each app made from the template picks its own.",
      "The backup extra was added in the app; add it to the compose file's x-winterflow.backups to make it part of the template.",
    ]);
  });

  it("notes nothing for an app that is already a good template", () => {
    const plain = { name: "Notes", files: [{ filename: "compose.yml" }], variables: [] };
    const { draft, notes } = templateFromApp("a1", {
      ...detail,
      app: { ...detail.app, config: b64(JSON.stringify(plain)), files: [{ name: "compose.yml", content: b64(COMPOSE) }], variables: [] },
      backups: [],
    });
    expect(notes).toEqual([]);
    expect(draft.color).toBe("#64748b");
  });

  it("notes that a Git source does not come along", () => {
    const git = { ...config, source: { repo_url: "https://example.com/r.git", branch: "main", auto_update: false } };
    const { notes } = templateFromApp("a1", { ...detail, app: { ...detail.app, config: b64(JSON.stringify(git)) } });
    expect(notes[0]).toContain("deploys from a Git repository");
  });

  it("slugs any app name into a template name", () => {
    expect(slugName("  2 Shop!! Front ")).toBe("shop-front");
    expect(slugName("123")).toBe("template");
    expect(slugName("a".repeat(80) + "-b")).toHaveLength(63);
    expect(slugName("a".repeat(62) + "-bbb--")).toBe("a".repeat(62));
  });
});
