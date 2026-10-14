import { describe, expect, it } from "vitest";

import { lowerFirst, relativeTime } from "@/lib/utils";

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
  it("reads a time slightly in the future (clock skew) as now", () => {
    expect(relativeTime(ago(-5), now)).toBe("now");
    expect(relativeTime(ago(-59), now)).toBe("now");
    expect(relativeTime(ago(-120), now)).toBe("in 2 minutes");
  });
  it("falls back to the date after a week", () => {
    const old = ago(8 * 86_400);
    expect(relativeTime(old, now)).toBe(old.toLocaleDateString());
  });
});

describe("lowerFirst", () => {
  it("puts a label inside a sentence, keeping acronyms", () => {
    expect(lowerFirst("Temporary password")).toBe("temporary password");
    expect(lowerFirst("Memory")).toBe("memory");
    expect(lowerFirst("CPU")).toBe("CPU");
    expect(lowerFirst("S3 bucket")).toBe("S3 bucket");
    expect(lowerFirst("")).toBe("");
    expect(lowerFirst("a")).toBe("a");
  });
});
