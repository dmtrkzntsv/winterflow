import { describe, expect, it } from "vitest";

import { relativeTime } from "@/lib/utils";

describe("relativeTime", () => {
  const now = 1_800_000_000_000;
  const ago = (secs: number) => new Date(now - secs * 1000);
  it("reads ages up to a week as relative labels", () => {
    expect(relativeTime(ago(0), now)).toBe("now");
    expect(relativeTime(ago(10), now)).toBe("10 seconds ago");
    expect(relativeTime(ago(240), now)).toBe("4 minutes ago");
    expect(relativeTime(ago(7200), now)).toBe("2 hours ago");
    expect(relativeTime(ago(86_400), now)).toBe("yesterday");
    expect(relativeTime(ago(3 * 86_400), now)).toBe("3 days ago");
  });
  it("falls back to the date after a week", () => {
    const old = ago(8 * 86_400);
    expect(relativeTime(old, now)).toBe(old.toLocaleDateString());
  });
});
