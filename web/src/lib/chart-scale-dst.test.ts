import { afterAll, beforeAll, describe, expect, it } from "vitest";

import { timeTicks } from "@/lib/chart-scale";

// Europe/Berlin leaves DST on 2026-10-25 (03:00 CEST -> 02:00 CET).
const ZONE = "Europe/Berlin";
// Node reads TZ at runtime; no @types/node here, so reach it via globalThis.
const env = (globalThis as unknown as { process: { env: Record<string, string | undefined> } }).process.env;
let prevTZ: string | undefined;
beforeAll(() => {
  prevTZ = env.TZ;
  env.TZ = ZONE;
});
afterAll(() => {
  if (prevTZ === undefined) delete env.TZ;
  else env.TZ = prevTZ;
});

const at = (iso: string) => Date.parse(iso);
const local = (t: number) =>
  new Intl.DateTimeFormat("en-GB", {
    timeZone: ZONE, year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", hourCycle: "h23",
  }).format(t);

describe("timeTicks across a DST change", () => {
  it("keeps 7d ticks on local midnights with no duplicate dates", () => {
    const ticks = timeTicks(at("2026-10-22T10:00:00Z"), at("2026-10-29T10:00:00Z"));
    expect(ticks.length).toBeGreaterThan(5);
    const labels = ticks.map(local);
    expect(labels.every((l) => l.endsWith("00:00"))).toBe(true);
    expect(new Set(labels.map((l) => l.slice(0, 10))).size).toBe(ticks.length);
  });

  it("keeps 24h ticks on round local hours across the change", () => {
    const ticks = timeTicks(at("2026-10-24T20:00:00Z"), at("2026-10-25T20:00:00Z"));
    const labels = ticks.map(local);
    expect(labels.every((l) => /^\d\d\/\d\d\/\d{4}, (\d\d):00$/.test(l) && Number(l.slice(12, 14)) % 3 === 0)).toBe(true);
    expect(new Set(labels).size).toBe(labels.length);
    expect(ticks.slice(1).every((t, i) => t > ticks[i])).toBe(true);
  });

  it("aligns weekly steps to local Monday midnight", () => {
    const ticks = timeTicks(at("2026-08-01T00:00:00Z"), at("2026-10-31T00:00:00Z"), 8);
    expect(ticks.length).toBeGreaterThan(2);
    for (const t of ticks) {
      expect(local(t).endsWith("00:00")).toBe(true);
      expect(new Date(t).getDay()).toBe(1);
    }
  });
});
