import { describe, expect, it } from "vitest";

import { tokenMeta } from "@/lib/tokens";

const day = (iso: string) => new Date(iso).toLocaleDateString();
const NOW = Date.parse("2026-10-13T12:00:00Z");

describe("tokenMeta", () => {
  it("says when a token was made, when it ends and when it was last used", () => {
    expect(
      tokenMeta({ created_at: "2026-10-01T00:00:00Z", expires_at: "2027-10-01T00:00:00Z", last_used_at: "2026-10-13T11:00:00Z" }, NOW),
    ).toBe(`Created ${day("2026-10-01T00:00:00Z")} · Expires ${day("2027-10-01T00:00:00Z")} · Last used 1 hour ago`);
  });

  it("says never, and expired", () => {
    expect(tokenMeta({ created_at: "2026-10-01T00:00:00Z", expires_at: null, last_used_at: null }, NOW)).toBe(
      `Created ${day("2026-10-01T00:00:00Z")} · Never expires · Never used`,
    );
    expect(tokenMeta({ created_at: "2026-01-01T00:00:00Z", expires_at: "2026-02-01T00:00:00Z", last_used_at: null }, NOW)).toBe(
      `Created ${day("2026-01-01T00:00:00Z")} · Expired ${day("2026-02-01T00:00:00Z")} · Never used`,
    );
  });
});
