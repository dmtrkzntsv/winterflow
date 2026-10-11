import { describe, expect, it } from "vitest";

import { domainNameError, expiry, isWildcard, TLS_MODES, validHostname } from "@/lib/domains";

describe("domain registrations", () => {
  it("validates names like the agent", () => {
    for (const ok of ["twillingate.app", "*.twillingate.app", "*.eu.twillingate.app", "localhost"]) {
      expect(domainNameError(ok)).toBe("");
    }
    for (const bad of ["", "*", "*.", "**.x.app", "a.*.x.app", "X.app", "x.app.", "x..app", "https://x.app", "*.*.x.app"]) {
      expect(domainNameError(bad)).not.toBe("");
    }
    expect(domainNameError(`${"a".repeat(250)}.app`)).not.toBe("");
    // The 253 cap counts the "*." too.
    const host = `${"a".repeat(62)}.${"b".repeat(62)}.${"c".repeat(62)}.${"d".repeat(59)}.app`;
    expect(validHostname(host)).toBe(true);
    expect(domainNameError(`*.${host}`)).not.toBe("");
    expect(isWildcard("*.x.app")).toBe(true);
    expect(isWildcard("x.app")).toBe(false);
  });

  it("checks hostname syntax", () => {
    expect(validHostname("shop.twillingate.app")).toBe(true);
    for (const bad of ["", ".twillingate.app", "*.x.app", "Shop.app", "a_b.app", "-x.app", `${"a".repeat(250)}.app`]) {
      expect(validHostname(bad)).toBe(false);
    }
  });

  it("words every mode", () => {
    expect(Object.keys(TLS_MODES)).toEqual(["auto", "certificate", "off"]);
    expect(TLS_MODES.off.label).toBe("TLS off");
  });

  it("describes certificate expiry", () => {
    const now = new Date("2026-10-10T12:00:00Z");
    expect(expiry(undefined, now)).toBeNull();
    expect(expiry(null, now)).toBeNull();
    expect(expiry("garbage", now)).toBeNull();
    expect(expiry("2026-10-09T12:00:00Z", now)).toEqual({ grade: "expired", days: -1, day: "2026-10-09" });
    expect(expiry("2026-10-10T12:00:00Z", now)).toMatchObject({ grade: "expired", days: 0 });
    // Rounds up: 10 days minus a second still reads 10.
    expect(expiry("2026-10-20T11:59:59Z", now)).toEqual({ grade: "soon", days: 10, day: "2026-10-20" });
    expect(expiry("2027-01-01T00:00:00Z", now)).toMatchObject({ grade: "ok", day: "2027-01-01" });
  });
});
