import { describe, expect, it } from "vitest";

import { buttonVariants } from "@/components/ui/button-variants";

describe("buttonVariants", () => {
  it("rings a focused filled button 2px off its edge, where its own colour can't hide it", () => {
    for (const variant of ["default", "destructive"] as const) {
      expect(buttonVariants({ variant })).toContain("focus-visible:ring-2");
      expect(buttonVariants({ variant })).toContain("focus-visible:ring-offset-2");
    }
    expect(buttonVariants({ variant: "ghost" })).not.toContain("focus-visible:ring-offset-2");
  });

  it("drops the shadow of a disabled button, so it doesn't read as one that works", () => {
    expect(buttonVariants()).toContain("disabled:shadow-none");
    expect(buttonVariants()).toContain("disabled:opacity-50");
  });
});
