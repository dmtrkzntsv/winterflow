import { describe, expect, it } from "vitest";

import {
  INBOX_PATH,
  LEGACY_APP_TABS,
  NEW_TEMPLATE_PATH,
  SETTINGS_PATH,
  TEMPLATES_PATH,
  appPath,
  appSub,
  appsPath,
  isWidePath,
  legacyAlertsTarget,
  legacyServerSettingsTarget,
  monitoringPath,
  newAppPath,
  newAppSub,
  serverSettingsPath,
  serverSettingsSub,
  settingsPath,
  templatePath,
  withQuery,
} from "@/lib/routes";

describe("route builders", () => {
  it("builds the server-scoped pages", () => {
    expect(appsPath("s1")).toBe("/server/s1");
    expect(newAppPath("s1")).toBe("/server/s1/new");
    expect(newAppPath("s1", "template")).toBe("/server/s1/new?from=template");
    expect(newAppSub("git")).toBe("/new?from=git");
    expect(appPath("s1", "a1")).toBe("/server/s1/app/a1");
    expect(appPath("s1", "a1", "metrics")).toBe("/server/s1/app/a1?tab=metrics");
    expect(appSub("a b")).toBe("/app/a%20b");
    expect(monitoringPath("s1")).toBe("/server/s1/monitoring");
    expect(serverSettingsPath("s1")).toBe("/server/s1/settings/general");
    expect(serverSettingsPath("s1", "docker")).toBe("/server/s1/settings/docker");
    expect(serverSettingsSub("storages")).toBe("/settings/storages");
  });

  it("builds the org-wide pages", () => {
    expect(INBOX_PATH).toBe("/inbox");
    expect(TEMPLATES_PATH).toBe("/templates");
    expect(NEW_TEMPLATE_PATH).toBe("/templates/new");
    expect(SETTINGS_PATH).toBe("/settings");
    expect(templatePath("t/1")).toBe("/templates/t%2F1");
    expect(settingsPath("members")).toBe("/settings/members");
  });
});

describe("legacy URLs", () => {
  it.each([
    [null, "/server/s1/settings/general"],
    ["agent", "/server/s1/settings/general"],
    ["registries", "/server/s1/settings/docker"],
    ["networks", "/server/s1/settings/docker"],
    ["volumes", "/server/s1/settings/docker"],
    ["domains", "/server/s1/settings/domains"],
    ["storages", "/server/s1/settings/storages"],
    ["variables", "/settings/variables"],
    ["nonsense", "/server/s1/settings/general"],
  ])("sends the server settings tab %s to %s", (tab, target) => {
    expect(legacyServerSettingsTarget("s1", tab)).toBe(target);
  });

  it.each([
    [null, "/inbox"],
    ["history", "/inbox"],
    ["rules", "/settings/notifications"],
    ["channels", "/settings/notifications"],
  ])("sends the alerts tab %s to %s", (tab, target) => {
    expect(legacyAlertsTarget(tab)).toBe(target);
  });

  it("renames the app page's Editor tab to Configuration", () => {
    expect(LEGACY_APP_TABS.editor).toBe("configuration");
    expect(LEGACY_APP_TABS.history).toBe("configuration");
  });

  it("carries the old query and hash over, merged into the target's own query (its params win)", () => {
    expect(withQuery("/settings/members", "?x=1", "#top")).toBe("/settings/members?x=1#top");
    expect(withQuery("/server/s1/new?from=template", "?x=1", "")).toBe("/server/s1/new?from=template&x=1");
    expect(withQuery("/server/s1/new?from=template", "?from=git&x=1", "")).toBe("/server/s1/new?from=template&x=1");
    expect(withQuery("/server/s1/new?from=template", "", "#h")).toBe("/server/s1/new?from=template#h");
    expect(withQuery("/inbox", "", "")).toBe("/inbox");
  });
});

describe("isWidePath", () => {
  it("lets the app page and Monitoring use the full width", () => {
    expect(isWidePath("/server/s1/app/a1")).toBe(true);
    expect(isWidePath("/server/s1/monitoring")).toBe(true);
    expect(isWidePath("/server/s1")).toBe(false);
    expect(isWidePath("/server/s1/settings/general")).toBe(false);
    expect(isWidePath("/settings/members")).toBe(false);
  });
});
