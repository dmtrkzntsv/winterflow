import { describe, expect, it } from "vitest";

import { defaultChanges, newKey, requiredFirst, splitValues, upgradeNeeds, variableErrors } from "@/lib/template-values";
import type { InstanceView } from "@/lib/templates";
import type { DeclaredVariable } from "@/types/app-config";

const v = (name: string, extra: Partial<DeclaredVariable> = {}): DeclaredVariable => ({
  name,
  required: false,
  encrypted: false,
  default: null,
  services: ["app"],
  ...extra,
});

const VARS = [v("LANGUAGE", { default: "en" }), v("BASE_URL", { required: true }), v("SECRET", { required: true, encrypted: true })];

const inst = (extra: Partial<InstanceView> = {}): InstanceView => ({
  key: "k1",
  server_id: "s",
  app_id: "a",
  template_id: "t",
  template_name: "notes",
  revision: 1,
  name: "notes",
  values: { BASE_URL: "https://a.test" },
  secrets: { SECRET: "<encrypted>" },
  hosts: [],
  state: "created",
  status_code: 0,
  last_error: "",
  created_at: "",
  updated_at: "",
  ...extra,
});

describe("variableErrors", () => {
  it("blocks a missing required value and a plain $; a kept secret may stay blank", () => {
    expect(variableErrors(VARS, { LANGUAGE: "$x" })).toEqual({
      LANGUAGE: "A plain value can't contain $.",
      BASE_URL: "Enter a value: this variable is required.",
      SECRET: "Enter a value: this variable is required.",
    });
    expect(variableErrors(VARS, { BASE_URL: "https://a.test" }, ["SECRET"])).toEqual({});
    expect(variableErrors(null, {})).toEqual({});
  });
});

describe("splitValues and requiredFirst", () => {
  it("splits plain values from typed secrets, blank as empty", () => {
    expect(splitValues(VARS, { BASE_URL: "u", SECRET: "s" })).toEqual({
      plain: { LANGUAGE: "", BASE_URL: "u" },
      typed: { SECRET: "s" },
    });
    expect(splitValues(undefined, {})).toEqual({ plain: {}, typed: {} });
  });
  it("orders required variables first", () => {
    expect(requiredFirst(VARS).map((x) => x.name)).toEqual(["BASE_URL", "SECRET", "LANGUAGE"]);
    expect(requiredFirst(null)).toEqual([]);
  });
});

describe("upgradeNeeds", () => {
  const target = [...VARS, v("REGION", { required: true }), v("TOKEN", { required: true, encrypted: true })];
  it("asks only for required values the instance doesn't hold", () => {
    expect(upgradeNeeds(target, inst()).map((x) => x.name)).toEqual(["REGION", "TOKEN"]);
  });
  it("asks a pending instance for every required secret again", () => {
    expect(upgradeNeeds(target, inst({ state: "pending", values: null })).map((x) => x.name)).toEqual([
      "BASE_URL",
      "SECRET",
      "REGION",
      "TOKEN",
    ]);
    expect(upgradeNeeds(null, inst())).toEqual([]);
  });
});

describe("defaultChanges", () => {
  it("names the plain variables the instance leaves to the compose file whose value changes", () => {
    const to = [v("LANGUAGE", { default: "de" }), v("BASE_URL", { required: true, default: "x" }), v("NEW", { default: "1" })];
    expect(defaultChanges(VARS, to, inst())).toEqual([{ name: "LANGUAGE", from: "en", to: "de" }]);
    expect(defaultChanges(undefined, undefined, inst())).toEqual([]);
  });
});

describe("newKey", () => {
  it("is a fresh UUID v4 each time", () => {
    const a = newKey();
    expect(a).toMatch(/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
    expect(newKey()).not.toBe(a);
  });
});
