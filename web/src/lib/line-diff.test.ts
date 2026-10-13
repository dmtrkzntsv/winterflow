import { describe, expect, it } from "vitest";

import { diffLines, hunks } from "@/lib/line-diff";

const ops = (before: string, after: string) => diffLines(before, after).map((l) => l.op + l.text);

describe("diffLines", () => {
  it("marks changed lines between a common head and tail", () => {
    expect(ops("a\nb\nc", "a\nB\nc")).toEqual([" a", "-b", "+B", " c"]);
  });

  it("keeps the common lines of a reordered middle", () => {
    expect(ops("x\na\nb\nc\ny", "x\nb\nc\nd\ny")).toEqual([" x", "-a", " b", " c", "+d", " y"]);
  });

  it("handles empty sides and identical text", () => {
    expect(ops("", "a\nb")).toEqual(["+a", "+b"]);
    expect(ops("a", "")).toEqual(["-a"]);
    expect(ops("a\nb", "a\nb")).toEqual([" a", " b"]);
    expect(ops("", "")).toEqual([]);
  });

  it("falls back to remove-then-add past the table bound", () => {
    const big = (p: string) => Array.from({ length: 1500 }, (_, i) => `${p}${i}`).join("\n");
    const out = diffLines(big("a"), big("b"));
    expect(out.filter((l) => l.op === "-")).toHaveLength(1500);
    expect(out[0].op).toBe("-");
    expect(out[1500].op).toBe("+");
  });
});

describe("hunks", () => {
  it("keeps three lines of context and counts what it skips", () => {
    const before = Array.from({ length: 20 }, (_, i) => `l${i}`).join("\n");
    const after = before.replace("l10", "L10");
    const [h, ...rest] = hunks(diffLines(before, after));
    expect(rest).toEqual([]);
    expect(h.skipped).toBe(7);
    expect(h.lines.map((l) => l.op + l.text)).toEqual([" l7", " l8", " l9", "-l10", "+L10", " l11", " l12", " l13"]);
  });

  it("splits distant changes and has none for equal text", () => {
    const before = Array.from({ length: 30 }, (_, i) => `l${i}`).join("\n");
    const after = before.replace("l2\n", "x\n").replace("l25", "y");
    expect(hunks(diffLines(before, after)).map((h) => h.skipped)).toEqual([0, 16]);
    expect(hunks(diffLines("a", "a"))).toEqual([]);
  });
});
