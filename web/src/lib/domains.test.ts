import { describe, expect, it } from "vitest";

import {
  certExpiry,
  covers,
  domainNameError,
  isWildcard,
  winningDomain,
} from "@/lib/domains";

describe("domain registrations", () => {
  it("validates names like the agent", () => {
    for (const ok of ["twillingate.app", "*.twillingate.app", "*.eu.twillingate.app", "localhost"]) {
      expect(domainNameError(ok)).toBe("");
    }
    for (const bad of ["", "*", "*.", "**.x.app", "a.*.x.app", "X.app", "x.app.", "x..app", "https://x.app", "*.*.x.app"]) {
      expect(domainNameError(bad)).not.toBe("");
    }
    expect(domainNameError(`${"a".repeat(250)}.app`)).not.toBe("");
    expect(isWildcard("*.x.app")).toBe(true);
    expect(isWildcard("x.app")).toBe(false);
  });

  it("covers one label under a wildcard, exact names only themselves", () => {
    expect(covers("twillingate.app", "twillingate.app")).toBe(true);
    expect(covers("twillingate.app", "a.twillingate.app")).toBe(false);
    expect(covers("*.twillingate.app", "a.twillingate.app")).toBe(true);
    expect(covers("*.twillingate.app", "twillingate.app")).toBe(false);
    expect(covers("*.twillingate.app", "a.b.twillingate.app")).toBe(false);
    expect(covers("*.twillingate.app", ".twillingate.app")).toBe(false);
    expect(covers("*.Twillingate.App", "A.twillingate.app")).toBe(true);
  });

  it("lets an exact name win over a wildcard", () => {
    const names = ["*.twillingate.app", "app.twillingate.app"];
    expect(winningDomain(names, "app.twillingate.app")).toBe("app.twillingate.app");
    expect(winningDomain(names, "shop.twillingate.app")).toBe("*.twillingate.app");
    expect(winningDomain(names, "a.b.twillingate.app")).toBeNull();
    expect(winningDomain([], "x.app")).toBeNull();
  });

  it("grades certificate expiry", () => {
    const now = new Date("2026-10-10T00:00:00Z");
    expect(certExpiry(undefined, now)).toBeNull();
    expect(certExpiry("garbage", now)).toBeNull();
    expect(certExpiry("2026-10-09T00:00:00Z", now)).toBe("expired");
    expect(certExpiry("2026-10-20T00:00:00Z", now)).toBe("soon");
    expect(certExpiry("2027-01-01T00:00:00Z", now)).toBe("ok");
  });
});
