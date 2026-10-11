import { describe, expect, it } from "vitest";

import { navMain } from "@/lib/nav";

describe("navMain", () => {
  it("links the Monitoring page right after the Dashboard", () => {
    expect(navMain.map((i) => [i.title, i.url])).toEqual([
      ["Dashboard", "/"],
      ["Monitoring", "/monitoring"],
    ]);
  });
});
