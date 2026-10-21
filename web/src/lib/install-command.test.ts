import { describe, expect, it } from "vitest";

import { buildInstallCommand, buildReenrollCommand } from "@/lib/install-command";

describe("buildInstallCommand", () => {
  it("uses the API origin", () => {
    expect(buildInstallCommand("https://app.example.com")).toBe(
      "curl -fsSL https://get.winterflow.dev/agent | sudo bash -s -- --url https://app.example.com",
    );
  });
  it("drops a trailing slash", () => {
    expect(buildInstallCommand("https://app.example.com/")).toBe(buildInstallCommand("https://app.example.com"));
  });
});

describe("buildReenrollCommand", () => {
  it("adds --reenroll", () => {
    expect(buildReenrollCommand("https://app.example.com/")).toBe(
      "curl -fsSL https://get.winterflow.dev/agent | sudo bash -s -- --url https://app.example.com --reenroll",
    );
  });
});
