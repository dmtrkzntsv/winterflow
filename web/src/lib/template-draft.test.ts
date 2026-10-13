import { describe, expect, it } from "vitest";

import {
  bodyFromDraft,
  draftFromBody,
  draftProblem,
  emptyDraft,
  ingressRow,
  paramRow,
  recipeBackupNames,
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

describe("recipeBackupNames", () => {
  it("reads block and flow entries, quoted names, the override file too", () => {
    const d = valid();
    expect(recipeBackupNames(d)).toEqual(["db", "uploads"]);
    const id = "override";
    d.files.push({ id, filename: "compose.override.yml", is_encrypted: false });
    d.contents[id] = "x-winterflow:\n  backups: [{ name: logs, type: files, volume: data }, { name: db, type: sqlite }]\n";
    expect(recipeBackupNames(d)).toEqual(["db", "uploads", "logs"]);
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
