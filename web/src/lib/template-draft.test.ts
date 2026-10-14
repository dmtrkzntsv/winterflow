import { describe, expect, it } from "vitest";

import type { AppDetailPayload } from "@/context/apps-context-base";
import type { EffectiveBackup } from "@/lib/backups";
import {
  backupNamesIn,
  bodyFromDraft,
  composeContents,
  draftFromBody,
  draftProblem,
  emptyDraft,
  ingressRow,
  paramRow,
  recipeBackupNames,
  slugName,
  templateFromApp,
  type TemplateDraft,
} from "@/lib/template-draft";
import type { TemplateBody } from "@/lib/templates";

const COMPOSE = `services:
  app:
    image: ghcr.io/dmtrkzntsv/twillingate:\${VERSION}
    ports: ["127.0.0.1:\${PORT}:8080"]
    volumes: [data:/data]
volumes: { data: {} }
x-winterflow:
  backups:
    - { name: db, type: sqlite, volume: data, path: twillingate.db, every: 1h }
    # a comment between items
    - name: "uploads"
      type: files
      volume: data
`;

const BODY: TemplateBody = {
  files: { "compose.yaml": COMPOSE, "Caddyfile": ":80" },
  parameters: [
    { name: "NAME", required: true, pattern: "^[a-z]{3,20}$", description: "Instance name" },
    { name: "PORT", required: true },
    { name: "MEMORY_MAX", default: "256m" },
    { name: "SESSION_KEY", secret: true, required: true },
  ],
  app_name: "${NAME}",
  icon: "chart",
  color: "#2563eb",
  ingress: [{ host: "${NAME}.twillingate.app", service: "app", port: "${PORT}" }],
  backups: { db: { enabled: true, storage: "tenants" }, gone: { enabled: true } },
};

function valid(): TemplateDraft {
  return draftFromBody("twillingate", BODY);
}

describe("draftFromBody / bodyFromDraft", () => {
  it("round-trips a body, dropping defaults of backups the compose file no longer declares", () => {
    const body = bodyFromDraft(valid());
    expect(body).toEqual({ ...BODY, backups: { db: { enabled: true, storage: "tenants" } } });
  });

  it("writes a numeric port as a number and leaves out empty fields", () => {
    const d = valid();
    d.ingress = [ingressRow("static.example.com", "8080")];
    d.parameters = [paramRow({ name: "X", default: "" })];
    const body = bodyFromDraft(d);
    expect(body.ingress).toEqual([{ host: "static.example.com", port: 8080 }]);
    expect(body.parameters).toEqual([{ name: "X" }]);
  });

  it("reads null lists from the API", () => {
    const d = draftFromBody("x", { ...BODY, files: null, parameters: null, ingress: null, backups: null });
    expect([d.files, d.parameters, d.ingress, d.backups]).toEqual([[], [], [], {}]);
  });
});

describe("composeContents / backupNamesIn", () => {
  it("picks the compose files compose loads, and reads names only from those given", () => {
    const d = valid();
    const [main, override] = composeContents(d);
    expect(backupNamesIn([main, override])).toEqual(recipeBackupNames(d));
    expect(backupNamesIn([undefined, undefined])).toEqual([]);
    expect(composeContents({ ...d, files: [] })).toEqual([]);
  });
});

describe("recipeBackupNames", () => {
  it("reads block and flow entries, quoted names, the override file too", () => {
    const d = valid();
    expect(recipeBackupNames(d)).toEqual(["db", "uploads"]);
    const id = "override";
    d.files.push({ id, filename: "compose.override.yml", is_encrypted: false });
    d.contents[id] = "x-winterflow:\n  backups: [{ name: logs, type: files, volume: data }, { name: db, type: sqlite }]\n";
    expect(recipeBackupNames(d)).toEqual(["db", "uploads", "logs"]);
  });

  it("reads a compose file that starts with a document marker", () => {
    const d = valid();
    d.contents[d.files.find((f) => f.filename === "compose.yaml")!.id] =
      "---\nservices: {}\nx-winterflow:\n  backups:\n    - { name: db, type: sqlite }\n";
    expect(recipeBackupNames(d)).toEqual(["db"]);
  });

  it("finds none without a compose file or a recipe", () => {
    expect(recipeBackupNames(emptyDraft())).toEqual([]);
    const d = valid();
    d.files = d.files.filter((f) => f.filename === "Caddyfile");
    expect(recipeBackupNames(d)).toEqual([]);
    const odd = valid();
    odd.contents[odd.files.find((f) => f.filename === "compose.yaml")!.id] = "x-winterflow:\n  backups: none\n";
    expect(recipeBackupNames(odd)).toEqual([]);
  });
});

describe("draftProblem", () => {
  const cases: [string, (d: TemplateDraft) => void, string][] = [
    ["name", (d) => (d.name = "Shop"), "Name: lowercase"],
    ["app name", (d) => (d.appName = " "), "App name is required."],
    ["file name", (d) => (d.files[0].filename = ""), "Every file needs a filename."],
    ["duplicate file", (d) => (d.files[0].filename = "compose.yaml"), "Two files have the same name."],
    ["compose", (d) => (d.files = d.files.filter((f) => f.filename === "Caddyfile")), "A compose file is required"],
    ["param name", (d) => (d.parameters[0].name = "name"), 'Parameter "name": uppercase'],
    ["reserved", (d) => (d.parameters[0].name = "WF_X"), "Parameter WF_X is reserved."],
    ["twice", (d) => (d.parameters[1].name = "NAME"), "Parameter NAME is declared twice."],
    ["long pattern", (d) => (d.parameters[0].pattern = "a".repeat(513)), "longer than 512 bytes"],
    ["dollar default", (d) => (d.parameters[2].default = "$X"), "the default may not contain $."],
    ["default vs pattern", (d) => (d.parameters[0].default = "AB"), "the default doesn't match the pattern."],
    ["app name ref", (d) => (d.appName = "${SESSION_KEY}"), "${SESSION_KEY} is not a plain parameter"],
    ["empty host", (d) => (d.ingress[0].host = ""), "Every ingress row needs a host."],
    ["host ref", (d) => (d.ingress[0].host = "${NOPE}.example.com"), "${NOPE} is not a plain parameter"],
    ["port", (d) => (d.ingress[0].port = "70000"), "the port is a number 1-65535"],
    ["port ref", (d) => (d.ingress[0].port = "${SESSION_KEY}"), "the port is a number 1-65535"],
  ];
  it.each(cases)("names the problem: %s", (_, change, want) => {
    const d = valid();
    change(d);
    expect(draftProblem(d)).toContain(want);
  });

  it("accepts a valid draft", () => {
    expect(draftProblem(valid())).toBeNull();
  });
});

describe("templateFromApp", () => {
  const b64 = (s: string) => btoa(s);
  const config = {
    name: "Shop Front",
    icon: "cart",
    color: "#111111",
    files: [{ filename: "compose.yml" }, { filename: "key.pem", is_encrypted: true }],
    variables: [{ name: "NAME" }, { name: "TOKEN", is_encrypted: true }, { name: "PRICE" }],
    ingress: {
      domains: [{ domain: "shop.example.com", upstream_port: 8080 }],
      redirects: [{ domain: "www.shop.example.com", to: "https://shop.example.com", code: 301 }],
    },
  };
  const backup = (b: Partial<EffectiveBackup>): EffectiveBackup =>
    ({ name: "db", type: "sqlite", enabled: true, storage: "tenants", in_recipe: true, from_recipe: { every: true }, ...b }) as EffectiveBackup;
  const detail: AppDetailPayload = {
    app: {
      app_id: "a1",
      config: b64(JSON.stringify(config)),
      files: [
        { name: "compose.yml", content: b64(COMPOSE) },
        { name: "key.pem", content: "<encrypted>", encrypted: true },
      ],
      variables: [
        { name: "NAME", content: b64("shop") },
        { name: "TOKEN", content: "<encrypted>", encrypted: true },
        { name: "PRICE", content: b64("$5") },
      ],
    },
    backups: [backup({}), backup({ name: "extra", in_recipe: false })],
    revision: 1,
    available_revisions: null,
  };

  it("builds revision 1 from what the editor holds", () => {
    const { draft, notes } = templateFromApp("a1", detail);
    expect(draft.name).toBe("shop-front");
    const body = bodyFromDraft(draft);
    expect(body).toEqual({
      files: { "compose.yml": COMPOSE },
      parameters: [{ name: "NAME", default: "shop" }, { name: "TOKEN", secret: true, required: true }, { name: "PRICE" }],
      app_name: "Shop Front",
      icon: "cart",
      color: "#111111",
      ingress: [{ host: "shop.example.com", port: 8080 }],
      // The backup added in the app's Backups tab has no recipe entry.
      backups: { db: { enabled: true, storage: "tenants" } },
    });
    expect(notes).toEqual([
      "key.pem is a secret file; a template holds no secrets, so it was left out.",
      "PRICE's value has a $, which a parameter can't hold; it has no default now.",
      "Redirects aren't part of templates; they were left out.",
      // The compose file has its own x-winterflow block: not edited.
      "The backup extra was added in the app; add it to the compose file's x-winterflow.backups to make it part of the template.",
    ]);
  });

  it("turns literal host ports into PORT parameters and carries a backup added in the app", () => {
    const compose = `services:
  web:
    image: nginx
    ports:
      - "8090:80"
      - 127.0.0.1:8091:80
    volumes: [data:/data]
  admin:
    ports: ["8090:81", "\${ADMIN}:82"]
volumes: { data: {} }
`;
    const app = {
      ...config,
      files: [{ filename: "compose.yml" }, { filename: "notes.txt" }],
      variables: [{ name: "PORT" }],
      ingress: { domains: [{ domain: "notes.example.com", upstream_port: 8091 }], redirects: [] },
    };
    const added = backup({
      name: "notes-data",
      type: "files",
      in_recipe: false,
      from_recipe: null,
      volume: "data",
      paths: ["uploads"],
      every: "24h",
      storage: "disk",
    });
    const { draft, notes } = templateFromApp("a1", {
      ...detail,
      app: {
        ...detail.app,
        config: b64(JSON.stringify(app)),
        files: [
          { name: "compose.yml", content: b64(compose) },
          { name: "notes.txt", content: b64("8090:80 stays") },
        ],
        variables: [{ name: "PORT", content: b64("x") }],
      },
      backups: [added],
    });
    const body = bodyFromDraft(draft);
    // PORT is the app's own variable: the ports take PORT_2 and PORT_3.
    expect(body.parameters).toEqual([
      { name: "PORT", default: "x" },
      { name: "PORT_2", required: true, default: "8090", description: "Host port for web" },
      { name: "PORT_3", required: true, default: "8091", description: "Host port for web" },
    ]);
    expect(body.files?.["compose.yml"]).toBe(`services:
  web:
    image: nginx
    ports:
      - "\${PORT_2}:80"
      - "127.0.0.1:\${PORT_3}:80"
    volumes: [data:/data]
  admin:
    ports: ["\${PORT_2}:81", "\${ADMIN}:82"]
volumes: { data: {} }

x-winterflow:
  backups:
    - {"name":"notes-data","type":"files","volume":"data","paths":["uploads"]}
`);
    // Only compose files change.
    expect(body.files?.["notes.txt"]).toBe("8090:80 stays");
    expect(body.ingress).toEqual([{ host: "notes.example.com", port: "${PORT_3}" }]);
    expect(body.backups).toEqual({ "notes-data": { enabled: true, storage: "disk", every: "24h" } });
    expect(recipeBackupNames(draft)).toEqual(["notes-data"]);
    expect(notes).toContain("Port 8090 is now the PORT_2 parameter, so each app can publish its own.");
    expect(draftProblem(draft)).toBeNull();
  });

  it("leaves port ranges and YAML files other than compose files as they are", () => {
    const compose = `services:
  media:
    ports:
      - "8000-8010:8000-8010"
      - "9000:9000"
`;
    const { draft, notes } = templateFromApp("a1", {
      ...detail,
      app: {
        ...detail.app,
        config: b64(JSON.stringify({ name: "Media", files: [{ filename: "compose.yml" }, { filename: "config/app.yml" }] })),
        files: [
          { name: "compose.yml", content: b64(compose) },
          { name: "config/app.yml", content: b64('ports: ["7000:7000"]') },
        ],
        variables: [],
      },
      backups: [],
    });
    const body = bodyFromDraft(draft);
    // One parameter, for the single port: the range is never one.
    expect(body.parameters).toEqual([{ name: "PORT", required: true, default: "9000", description: "Host port for media" }]);
    expect(body.files?.["compose.yml"]).toContain('- "8000-8010:8000-8010"');
    expect(body.files?.["compose.yml"]).toContain('- "${PORT}:9000"');
    expect(body.files?.["config/app.yml"]).toBe('ports: ["7000:7000"]');
    expect(notes).toContain("Ports 8000–8010 are a range, which stays as it is: two apps from the template would both publish them.");
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

describe("a new template", () => {
  it("publishes ${PORT}, a parameter defaulting to 8080, so each app picks its own port", () => {
    const body = bodyFromDraft({ ...emptyDraft(), name: "site", appName: "Site" });
    expect(Object.values(body.files ?? {})[0]).toContain('- "${PORT}:80"');
    expect(body.parameters).toEqual([{ name: "PORT", default: "8080", description: "Host port" }]);
  });
});
