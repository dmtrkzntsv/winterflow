import { describe, expect, it } from "vitest";

import { settingsSections } from "@/lib/settings-nav";

const flat = (sections: ReturnType<typeof settingsSections>) =>
  sections.map((s) => [s.title, s.items.map((i) => [i.label, i.to])]);

describe("settingsSections", () => {
  it("lists the server's sections, then the organization's, then the account's", () => {
    expect(flat(settingsSections({ server: { id: "s1", name: "homelab", hasIngress: true }, hasPassword: true }))).toEqual([
      [
        "Server · homelab",
        [
          ["General", "/server/s1/settings/general"],
          ["Domains", "/server/s1/settings/domains"],
          ["Storages", "/server/s1/settings/storages"],
          ["Docker", "/server/s1/settings/docker"],
        ],
      ],
      [
        "Organization",
        [
          ["General", "/settings/organization"],
          ["Members", "/settings/members"],
          ["Shared variables", "/settings/variables"],
          ["Notifications", "/settings/notifications"],
        ],
      ],
      [
        "Account",
        [
          ["Password", "/settings/password"],
          ["API tokens", "/settings/tokens"],
        ],
      ],
    ]);
  });

  it("offers Domains only on a server with ingress", () => {
    const [server] = settingsSections({ server: { id: "s1", name: "homelab", hasIngress: false }, hasPassword: true });
    expect(server.items.map((i) => i.label)).toEqual(["General", "Storages", "Docker"]);
  });

  it("has no server section without a server, and no Password without local credentials", () => {
    const sections = settingsSections({ server: null, hasPassword: false });
    expect(sections.map((s) => s.title)).toEqual(["Organization", "Account"]);
    expect(sections[1].items.map((i) => i.label)).toEqual(["API tokens"]);
  });
});

describe("settingsSections, account", () => {
  it("adds Sign-in methods with providers and Connected apps with the OAuth server", () => {
    const sections = settingsSections({ server: null, hasPassword: true, hasProviders: true, oauthServer: true });
    expect(sections[1].items.map((i) => [i.label, i.to])).toEqual([
      ["Password", "/settings/password"],
      ["Sign-in methods", "/settings/identities"],
      ["API tokens", "/settings/tokens"],
      ["Connected apps", "/settings/connected-apps"],
    ]);
  });
});
