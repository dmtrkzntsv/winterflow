import { describe, expect, it } from "vitest";

import { templateSearch } from "@/lib/template-search";
import type { TemplateSummary } from "@/lib/templates";

const base: TemplateSummary = {
  id: "org",
  source: "org",
  slug: "",
  name: "Team stack",
  latest_revision: 1,
  instances: 0,
  behind: 0,
  withdrawn_at: null,
  updated_at: "",
};
const catalog: TemplateSummary = {
  ...base,
  id: "catalog",
  source: "catalog",
  slug: "actual-budget",
  name: "Actual Budget",
  catalog: {
    summary: "Plan your money",
    description: "Current description",
    keywords: ["finance"],
    tags: [],
    alternative_to: ["Mint"],
    license: "MIT",
    homepage_url: "",
    repository_url: "",
    documentation_url: "",
    sponsorship: [],
    logo_url: "",
  },
};

describe("templateSearch", () => {
  const list = [base, catalog];
  const looks = { org: { icon: "Box", color: "", summary: "Internal analytics" } };

  it("keeps source order for an empty query and searches org summaries", () => {
    expect(templateSearch(list, "  ", looks)).toEqual(list);
    expect(templateSearch(list, "ANALYTICS", looks)).toEqual([base]);
  });

  it("searches live catalog summaries, keywords and alternative-to", () => {
    for (const query of ["actual", "money", "FINANCE", "mint"]) {
      expect(templateSearch(list, query, looks)).toEqual([catalog]);
    }
    expect(templateSearch(list, "current description", looks)).toEqual([]);
  });

  it("keeps a template usable when its optional metadata is missing", () => {
    expect(templateSearch([{ ...catalog, catalog: null }], "actual")).toHaveLength(1);
    expect(templateSearch([{ ...catalog, catalog: null }], "finance")).toEqual([]);
  });
});
