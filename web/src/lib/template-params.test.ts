import { describe, expect, it } from "vitest";

import {
  compilePattern,
  expand,
  fieldErrors,
  newKey,
  plainValues,
  previewHosts,
  requiredFirst,
  upgradeNeeds,
  valueError,
} from "@/lib/template-params";
import type { InstanceView, Parameter, TemplateBody } from "@/lib/templates";

const NAME: Parameter = { name: "NAME", required: true, pattern: "^[a-z]{3,20}$" };
const PORT: Parameter = { name: "PORT", required: true, pattern: "^[1-9][0-9]{0,4}$" };
const MEM: Parameter = { name: "MEMORY_MAX", default: "256m" };
const KEY: Parameter = { name: "SESSION_KEY", secret: true, required: true };

describe("compilePattern", () => {
  it("compiles what JavaScript reads like RE2", () => {
    expect(compilePattern("^[a-z]{3,20}$")?.test("shop")).toBe(true);
    expect(compilePattern("(?i)^shop$")?.test("SHOP")).toBe(true);
    expect(compilePattern("(?is)^a.b$")?.test("A\nB")).toBe(true);
  });

  it("leaves RE2-only patterns, and ones JavaScript refuses, to the API", () => {
    for (const p of ["[[:alpha:]]+", "\\pL+", "^a\\z", "(?P<x>a)", "(?U)a+", "(?i-s)a", "(", ""]) {
      expect(compilePattern(p)).toBeNull();
    }
    expect(compilePattern(undefined)).toBeNull();
  });
});

describe("valueError", () => {
  it("checks a value as typed", () => {
    expect(valueError(NAME, "shop")).toBe("");
    expect(valueError(NAME, "Shop!")).toBe("Must match ^[a-z]{3,20}$");
    expect(valueError(NAME, "")).toBe("Required");
    expect(valueError({ name: "X", required: true, default: "a" }, "")).toBe("");
    expect(valueError(MEM, "${HOME}")).toBe("May not contain $");
    // A pattern JavaScript can't check is the API's to check.
    expect(valueError({ name: "X", pattern: "[[:alpha:]]+" }, "123")).toBe("");
  });
});

describe("fieldErrors", () => {
  it("collects each field's problem; a kept secret may stay blank", () => {
    expect(fieldErrors([NAME, MEM, KEY], { NAME: "X" })).toEqual({ NAME: "Must match ^[a-z]{3,20}$", SESSION_KEY: "Required" });
    expect(fieldErrors([NAME, KEY], { NAME: "shop" }, ["SESSION_KEY"])).toEqual({});
    expect(fieldErrors([{ name: "T", secret: true }], {})).toEqual({});
    expect(fieldErrors(null, {})).toEqual({});
  });
});

describe("rendering", () => {
  const body: TemplateBody = {
    files: {},
    parameters: [NAME, PORT, MEM, KEY],
    app_name: "${NAME}",
    icon: "",
    color: "",
    ingress: [{ host: "${NAME}.twillingate.app", port: "${PORT}" }, { host: "static.example.com", port: 8080 }],
    backups: null,
  };

  it("orders required parameters first", () => {
    expect(requiredFirst([MEM, NAME, KEY]).map((p) => p.name)).toEqual(["NAME", "SESSION_KEY", "MEMORY_MAX"]);
    expect(requiredFirst(null)).toEqual([]);
  });

  it("uses given values, then defaults, never secrets", () => {
    expect(plainValues(body.parameters, { NAME: "shop" })).toEqual({ NAME: "shop", PORT: "", MEMORY_MAX: "256m" });
    expect(plainValues(null, {})).toEqual({});
    expect(expand("${NAME}-${NOPE}", { NAME: "shop" })).toBe("shop-");
  });

  it("previews the hosts, naming what is still missing", () => {
    expect(previewHosts(body, plainValues(body.parameters, { NAME: "shop" }))).toEqual([
      { host: "shop.twillingate.app", port: "", missing: ["PORT"] },
      { host: "static.example.com", port: "8080", missing: [] },
    ]);
    expect(previewHosts({ ...body, ingress: null }, {})).toEqual([]);
  });
});

describe("upgradeNeeds", () => {
  const inst = {
    state: "created",
    parameters: { NAME: "shop" },
    secrets: { SESSION_KEY: "<encrypted>" },
  } as unknown as InstanceView;
  const target: Parameter[] = [NAME, MEM, KEY, { name: "REGION", required: true }, { name: "TOKEN", secret: true, required: true }];

  it("asks only for required values the instance doesn't hold", () => {
    expect(upgradeNeeds(target, inst).map((p) => p.name)).toEqual(["REGION", "TOKEN"]);
  });

  it("asks a pending instance for every required secret again", () => {
    expect(upgradeNeeds(target, { ...inst, state: "pending" }).map((p) => p.name)).toEqual([
      "SESSION_KEY",
      "REGION",
      "TOKEN",
    ]);
    expect(upgradeNeeds(null, inst)).toEqual([]);
    expect(upgradeNeeds(target, { ...inst, parameters: null, secrets: null }).map((p) => p.name)).toEqual([
      "NAME",
      "SESSION_KEY",
      "REGION",
      "TOKEN",
    ]);
  });
});

describe("newKey", () => {
  it("is a fresh UUID v4 each time", () => {
    const a = newKey();
    expect(a).toMatch(/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
    expect(newKey()).not.toBe(a);
  });
});
