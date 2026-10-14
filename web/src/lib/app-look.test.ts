import { describe, expect, it } from "vitest";

import { getAppIcon } from "@/lib/app-icons";
import { APP_COLORS, DEFAULT_APP_COLOR, colorName, suggestAppLook } from "@/lib/app-look";

describe("suggestAppLook", () => {
  it("picks an icon from a word in the name", () => {
    expect(suggestAppLook("my-blog").icon).toBe("Book");
    expect(suggestAppLook("Postgres DB").icon).toBe("Database");
    expect(suggestAppLook("shop").icon).toBe("ShoppingCart");
    expect(suggestAppLook("jellyfin").icon).toBe("Film");
  });

  it("matches word starts only (feedback is not a database)", () => {
    expect(suggestAppLook("feedback").icon).toBe("");
  });

  it("matches the short stems doc, dev and stat as whole words only", () => {
    expect(suggestAppLook("docs").icon).toBe("Book");
    expect(suggestAppLook("my doc").icon).toBe("Book");
    expect(suggestAppLook("dev").icon).toBe("Code");
    expect(suggestAppLook("stats").icon).toBe("Activity");
    for (const name of ["docker-registry", "devices", "static-site-x", "state", "devops"]) {
      expect(suggestAppLook(name).icon, name).toBe(name === "static-site-x" ? "Globe" : "");
    }
  });

  it("names the palette's colours in plain words", () => {
    expect(APP_COLORS.map(colorName)).toEqual(["Sky", "Indigo", "Teal", "Green", "Amber", "Red", "Pink", "Slate"]);
    expect(colorName("#ABCDEF")).toBe("Custom");
    expect(colorName("#6366F1")).toBe("Indigo");
  });

  it("gives every name a stable colour from the palette", () => {
    const a = suggestAppLook("website").color;
    expect(APP_COLORS).toContain(a);
    expect(suggestAppLook("website").color).toBe(a);
    expect(suggestAppLook("  Website ").color).toBe(a);
  });

  it("falls back to the neutral tile for an empty name", () => {
    expect(suggestAppLook("  ")).toEqual({ icon: "", color: DEFAULT_APP_COLOR });
  });

  it("only suggests icons the picker has", () => {
    for (const name of ["blog", "shop", "db", "mail", "photos", "video", "music", "chat", "grafana", "cloud", "git", "home", "vault", "web"]) {
      const { icon } = suggestAppLook(name);
      expect(icon, name).not.toBe("");
      expect(getAppIcon(icon), name).toBeDefined();
    }
  });
});
