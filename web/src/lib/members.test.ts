import { describe, expect, it } from "vitest";

import { initials } from "@/lib/members";

describe("initials", () => {
  it("takes the first letters of the first two words, else of the email", () => {
    expect(initials("Ann Lee", "ann@example.test")).toBe("AL");
    expect(initials("bob", "bob@example.test")).toBe("B");
    expect(initials("", "cy.dee@example.test")).toBe("CD");
    expect(initials(" ", "")).toBe("?");
  });
});
