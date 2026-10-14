import { describe, expect, it } from "vitest";

import { errorHeadline } from "@/lib/errors";

describe("errorHeadline", () => {
  it("is the last non-empty line without the compose prefix", () => {
    expect(errorHeadline("docker compose up: exit status 1: no configuration file provided: not found")).toBe(
      "no configuration file provided: not found",
    );
    expect(
      errorHeadline("docker compose up: exit status 1: Container x  Creating\nBind for 0.0.0.0:8088 failed: port is already allocated\n"),
    ).toBe("Bind for 0.0.0.0:8088 failed: port is already allocated");
    expect(errorHeadline("  server offline  ")).toBe("server offline");
    expect(errorHeadline("")).toBe("");
  });

  it("keeps a line that is nothing but the prefix", () => {
    expect(errorHeadline("exit status 1: ")).toBe("exit status 1:");
  });
});
