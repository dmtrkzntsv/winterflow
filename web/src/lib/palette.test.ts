import { afterEach, describe, expect, it } from "vitest";

import { RECENT_KEY, paletteNavEntries, pushRecent, readRecent, visibleRecent } from "@/lib/palette";
import { settingsSections } from "@/lib/settings-nav";

const settings = settingsSections({ server: { id: "s1", name: "Alpha", hasIngress: false }, hasPassword: true });
const servers = [
  { id: "s1", name: "Alpha" },
  { id: "s2", name: "Beta" },
];

describe("paletteNavEntries", () => {
  it("offers active templates and uses detail pages when no server is selected", () => {
    const templates = [
      { id: "org1", source: "org" as const, slug: "org1", name: "Org stack", latest_revision: 1, instances: 0, behind: 0, withdrawn_at: null, updated_at: "" },
      { id: "cat1", source: "catalog" as const, slug: "cat1", name: "Catalog stack", latest_revision: 1, instances: 0, behind: 0, withdrawn_at: null, updated_at: "", catalog: { keywords: ["proxy"], alternative_to: ["nginx"], summary: "Reverse proxy", description: "", tags: [], license: "", homepage_url: "", repository_url: "", documentation_url: "", logo_url: "" } },
      { id: "cat2", source: "catalog" as const, slug: "cat2", name: "Gone", latest_revision: 1, instances: 1, behind: 0, withdrawn_at: "today", updated_at: "" },
    ];
    const withServer = paletteNavEntries({ serverId: "s1", apps: [], servers, settings, standalone: false, templates });
    expect(withServer.filter((e) => e.group === "Templates").map((e) => [e.label, e.to, e.keywords])).toEqual([
      ["Org stack", "/server/s1/new?from=template&template=org1", []],
      ["Catalog stack", "/server/s1/new?from=template&template=cat1", ["Reverse proxy", "proxy", "nginx"]],
    ]);
    expect(paletteNavEntries({ serverId: null, apps: [], servers, settings, standalone: false, templates }).filter((e) => e.group === "Templates").map((e) => e.to)).toEqual(["/templates/org1", "/templates/cat1"]);
  });
  it("lists this server's apps, every page, every Settings section, the other servers and New app", () => {
    const entries = paletteNavEntries({
      serverId: "s1",
      apps: [
        { id: "a1", name: "Shop", serverId: "s1" },
        { id: "0123456789", name: "", serverId: "s1" },
        { id: "b1", name: "Elsewhere", serverId: "s2" },
      ],
      servers,
      settings,
      standalone: false,
    });
    expect(entries.filter((e) => e.group === "Apps").map((e) => [e.label, e.to])).toEqual([
      ["Shop", "/server/s1/app/a1"],
      ["Unnamed app", "/server/s1/app/0123456789"],
    ]);
    expect(entries.filter((e) => e.group === "Pages").map((e) => [e.label, e.to])).toEqual([
      ["Apps", "/server/s1"],
      ["Monitoring", "/server/s1/monitoring"],
      ["Inbox", "/inbox"],
      ["Templates", "/templates"],
    ]);
    const general = entries.filter((e) => e.group === "Settings" && e.label === "General");
    expect(general.map((e) => [e.hint, e.to])).toEqual([
      ["Server · Alpha", "/server/s1/settings/general"],
      ["Organization", "/settings/organization"],
    ]);
    expect(entries.filter((e) => e.group === "Servers").map((e) => [e.label, e.to])).toEqual([["Beta", "/server/s2"]]);
    expect(entries.filter((e) => e.group === "Actions").map((e) => [e.label, e.to])).toEqual([["New app", "/server/s1/new"]]);
  });

  it("has no server pages without a server, and no servers on a standalone install", () => {
    const none = paletteNavEntries({ serverId: null, apps: [], servers: [], settings, standalone: false });
    expect(none.map((e) => e.label)).not.toContain("Monitoring");
    expect(none.map((e) => e.label)).not.toContain("New app");
    const standalone = paletteNavEntries({ serverId: "s1", apps: [], servers, settings, standalone: true });
    expect(standalone.some((e) => e.group === "Servers")).toBe(false);
  });

  it("offers no apps without an active server", () => {
    const entries = paletteNavEntries({ serverId: null, apps: [{ id: "a1", name: "Shop", serverId: "s1" }], servers, settings, standalone: false });
    expect(entries.some((e) => e.group === "Apps")).toBe(false);
  });
});

describe("recent items", () => {
  afterEach(() => localStorage.clear());

  it("keeps the last five, newest first, without repeats", () => {
    for (const n of [1, 2, 3, 4, 5, 6]) pushRecent({ id: `app:${n}`, label: `App ${n}`, to: `/x/${n}` });
    pushRecent({ id: "app:4", label: "App 4", to: "/x/4" });
    expect(readRecent().map((r) => r.id)).toEqual(["app:4", "app:6", "app:5", "app:3", "app:2"]);
  });

  it("reads garbage as no recent items", () => {
    localStorage.setItem(RECENT_KEY, "{not json");
    expect(readRecent()).toEqual([]);
    localStorage.setItem(RECENT_KEY, JSON.stringify({ id: "x" }));
    expect(readRecent()).toEqual([]);
    localStorage.setItem(RECENT_KEY, JSON.stringify([{ id: 1 }, { id: "page:inbox", label: "Inbox", to: "/inbox" }]));
    expect(readRecent()).toEqual([{ id: "page:inbox", label: "Inbox", to: "/inbox" }]);
  });

  it("shows only recent items that still exist, as they are now", () => {
    const entries = paletteNavEntries({
      serverId: "s1",
      apps: [{ id: "a1", name: "Shop v2", serverId: "s1" }],
      servers,
      settings,
      standalone: false,
    });
    const shown = visibleRecent(
      [
        { id: "app:gone", label: "Gone", to: "/server/s1/app/gone" },
        { id: "app:a1", label: "Shop", to: "/server/s1/app/a1" },
        { id: "page:inbox", label: "Inbox", to: "/inbox" },
      ],
      entries,
    );
    expect(shown.map((e) => e.label)).toEqual(["Shop v2", "Inbox"]);
  });
});
