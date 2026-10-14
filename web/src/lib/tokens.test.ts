import { describe, expect, it } from "vitest";

import { tokenMeta } from "@/lib/tokens";

const day = (iso: string) => new Date(iso).toLocaleDateString();
const NOW = Date.parse("2026-10-13T12:00:00Z");
// dates is how many calendar dates apart two times are, here.
const dates = (a: number, b: number) => {
  const day0 = (ms: number) => {
    const d = new Date(ms);
    return Date.UTC(d.getFullYear(), d.getMonth(), d.getDate());
  };
  return Math.round((day0(b) - day0(a)) / 86_400_000);
};

describe("tokenMeta", () => {
  it("says when a token was made, when it ends and when it was last used", () => {
    expect(
      tokenMeta({ created_at: "2026-10-01T00:00:00Z", expires_at: "2027-10-01T00:00:00Z", last_used_at: "2026-10-13T11:00:00Z" }, NOW),
    ).toBe(`Created ${day("2026-10-01T00:00:00Z")} · Expires in ${dates(NOW, Date.parse("2027-10-01T00:00:00Z"))} days · Last used 1 hour ago`);
    // A token made this week says when relatively; one ending soon says so in days.
    expect(tokenMeta({ created_at: "2026-10-12T12:00:00Z", expires_at: "2026-11-12T12:00:00Z", last_used_at: null }, NOW)).toBe(
      "Created yesterday · Expires in 30 days · Never used",
    );
  });

  it("says today only on the token's last calendar date, and tomorrow on the one before", () => {
    // Local times: 23:30 today, 01:00 tomorrow (two hours away), 22:00 the day after.
    const now = new Date(2026, 9, 13, 23, 0).getTime();
    const at = (d: number, h: number) => new Date(2026, 9, d, h, 0).toISOString();
    const created = new Date(now - 3_600_000).toISOString();
    expect(tokenMeta({ created_at: created, expires_at: new Date(now + 1_800_000).toISOString(), last_used_at: null }, now)).toBe(
      "Created 1 hour ago · Expires today · Never used",
    );
    expect(tokenMeta({ created_at: created, expires_at: at(14, 1), last_used_at: null }, now)).toBe(
      "Created 1 hour ago · Expires tomorrow · Never used",
    );
    expect(tokenMeta({ created_at: created, expires_at: at(15, 22), last_used_at: null }, now)).toBe(
      "Created 1 hour ago · Expires in 2 days · Never used",
    );
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
