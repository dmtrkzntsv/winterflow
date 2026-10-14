import { describe, expect, it } from "vitest";

import { revisionLabel, whenLabel } from "@/lib/revision-labels";

const rev = (hash: string, subject: string, timestamp = 1_790_000_000) => ({ hash, subject, timestamp });

describe("revisionLabel", () => {
  const history = [
    rev("ffff0000", "custom message from a migration"),
    rev("eeee0000", "update source to 5e6f7a8b"),
    rev("dddd0000", "rollback to aaaa0000"),
    rev("cccc0000", "rollback to 99990000"),
    rev("bbbb0000", "rename to Family Notes"),
    rev("abab0000", "update backups"),
    rev("acac0000", "save Notes"),
    rev("aaaa0000", "save Notes", 1_789_000_000),
  ];
  const label = (i: number) => revisionLabel(history, i, (d) => `@${d.getTime() / 1000}`);

  it("reads the first version as Created, whatever its subject", () => {
    expect(label(7)).toBe("Created");
    expect(revisionLabel([rev("a", "anything")], 0)).toBe("Created");
  });

  it("says what each known subject did", () => {
    expect(label(6)).toBe("Saved");
    expect(label(5)).toBe("Changed backups");
    expect(label(4)).toBe("Renamed to Family Notes");
    expect(label(1)).toBe("Updated from its source");
  });

  it("names when the version rolled back to was made, or just Rolled back when it isn't listed", () => {
    expect(label(2)).toBe("Rolled back to @1789000000");
    expect(label(3)).toBe("Rolled back");
  });

  it("shows an unknown subject as it is, and nothing for a missing index", () => {
    expect(label(0)).toBe("custom message from a migration");
    expect(revisionLabel(history, 99)).toBe("");
  });

  it("writes the date with the time", () => {
    expect(whenLabel(new Date(1_789_000_000_000))).toMatch(/\d/);
  });
});
