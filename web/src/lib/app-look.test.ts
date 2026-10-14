import { describe, expect, it } from "vitest";

import { getAppIcon } from "@/lib/app-icons";
import { APP_COLORS, DEFAULT_APP_COLOR, suggestAppLook } from "@/lib/app-look";

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
