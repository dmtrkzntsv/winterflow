import { describe, expect, it } from "vitest";

import { serverFacts, versionLabel } from "@/lib/server-facts";

describe("serverFacts", () => {
  it("lists what the agent reported, in order", () => {
    expect(
      serverFacts({
        server_ip: "203.0.113.7",
        system_cpu_cores: "4",
        system_memory_total: String(16 * 1024 ** 3),
        system_disk_total: String(512 * 1024 ** 3),
        version: "1.2.3",
      }),
    ).toEqual([
      { label: "IP address", value: "203.0.113.7" },
      { label: "CPU", value: "4 cores" },
      { label: "Memory", value: "16.0 GB" },
      { label: "Disk", value: "512.0 GB" },
    ]);
  });

  it("leaves out what was not reported, and says one core", () => {
    expect(serverFacts({})).toEqual([]);
    expect(serverFacts({ system_cpu_cores: "1", system_memory_total: "" })).toEqual([{ label: "CPU", value: "1 core" }]);
  });
});

describe("versionLabel", () => {
  it("writes a version with one v", () => {
    expect(versionLabel("1.2.3")).toBe("v1.2.3");
    expect(versionLabel("v261013.2")).toBe("v261013.2");
    expect(versionLabel(" ")).toBe("");
  });
});
