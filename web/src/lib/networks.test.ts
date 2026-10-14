import { describe, expect, it } from "vitest";

import { groupNetworks } from "@/lib/networks";

describe("groupNetworks", () => {
  it("puts each app's networks under its name, the rest under Other, the built-in ones apart", () => {
    const net = (name: string) => ({ id: `id-${name}`, name });
    const { groups, builtin } = groupNetworks(
      [
        net("bridge"),
        net("wf-a2_default"),
        net("wf-a1_default"),
        net("wf-a1_backend"),
        net("bridge-cse_01_default"),
        net("host"),
        net("wf-gone_default"),
        net("none"),
      ],
      [
        { id: "a1", name: "Website" },
        { id: "a2", name: "" },
        { id: "a3", name: "Notes" },
      ],
    );
    expect(builtin.map((n) => n.name)).toEqual(["bridge", "host", "none"]);
    expect(groups.map((g) => [g.label, g.appId, g.networks.map((n) => n.short)])).toEqual([
      ["Unnamed app", "a2", ["default"]],
      ["Website", "a1", ["default", "backend"]],
      ["Other", undefined, ["bridge-cse_01_default", "wf-gone_default"]],
    ]);
  });

  it("has no groups for no networks", () => {
    expect(groupNetworks([], [])).toEqual({ groups: [], builtin: [] });
  });
});
