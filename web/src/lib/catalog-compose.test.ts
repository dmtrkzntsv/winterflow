import { describe, expect, it } from "vitest";

import { catalogComposeExample } from "@/lib/catalog-compose";

describe("catalogComposeExample", () => {
  it("hides only the top-level Winterflow extension, even before another Compose section", () => {
    expect(catalogComposeExample("services:\n  app:\n    image: example\nx-winterflow:\n  variables:\n    TOKEN: { encrypted: true }\nvolumes:\n  data: {}\n"))
      .toBe("services:\n  app:\n    image: example\nvolumes:\n  data: {}\n");
  });

  it("keeps service extensions and accepts an inline Winterflow section", () => {
    expect(catalogComposeExample("services:\n  app:\n    x-winterflow: keep-me\nx-winterflow: { expose: [] }\n"))
      .toBe("services:\n  app:\n    x-winterflow: keep-me\n");
  });
});
