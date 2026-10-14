import { afterEach, describe, expect, it, vi } from "vitest";

import { isMacPlatform, isPaletteShortcut, modKeyLabel, serverShortcutIndex, shortcutLabel } from "@/lib/keyboard";

const key = (k: string, mods: Partial<Record<"metaKey" | "ctrlKey" | "altKey" | "shiftKey", boolean>> = {}) => ({
  key: k,
  metaKey: false,
  ctrlKey: false,
  altKey: false,
  shiftKey: false,
  ...mods,
});

describe("isPaletteShortcut", () => {
  it("is ⌘K or Ctrl+K", () => {
    expect(isPaletteShortcut(key("k", { metaKey: true }))).toBe(true);
    expect(isPaletteShortcut(key("K", { ctrlKey: true }))).toBe(true);
  });

  it("is not a plain K or one with Alt or Shift", () => {
    expect(isPaletteShortcut(key("k"))).toBe(false);
    expect(isPaletteShortcut(key("k", { metaKey: true, shiftKey: true }))).toBe(false);
    expect(isPaletteShortcut(key("k", { ctrlKey: true, altKey: true }))).toBe(false);
  });
});

describe("serverShortcutIndex", () => {
  it("maps ⌘1…⌘9 to the first nine servers", () => {
    expect(serverShortcutIndex(key("1", { metaKey: true }))).toBe(0);
    expect(serverShortcutIndex(key("9", { ctrlKey: true }))).toBe(8);
  });

  it("ignores 0, letters, bare digits and extra modifiers", () => {
    expect(serverShortcutIndex(key("0", { metaKey: true }))).toBeNull();
    expect(serverShortcutIndex(key("a", { metaKey: true }))).toBeNull();
    expect(serverShortcutIndex(key("1"))).toBeNull();
    expect(serverShortcutIndex(key("1", { metaKey: true, altKey: true }))).toBeNull();
    expect(serverShortcutIndex(key("1", { ctrlKey: true, shiftKey: true }))).toBeNull();
  });
});

describe("labels", () => {
  afterEach(() => vi.restoreAllMocks());

  it("says ⌘ on a Mac", () => {
    vi.spyOn(navigator, "platform", "get").mockReturnValue("MacIntel");
    expect(isMacPlatform()).toBe(true);
    expect(modKeyLabel()).toBe("⌘");
    expect(shortcutLabel(2)).toBe("⌘2");
  });

  it("says Ctrl+ elsewhere", () => {
    vi.spyOn(navigator, "platform", "get").mockReturnValue("Linux x86_64");
    vi.spyOn(navigator, "userAgent", "get").mockReturnValue("Mozilla/5.0 (X11; Linux x86_64)");
    expect(isMacPlatform()).toBe(false);
    expect(shortcutLabel("K")).toBe("Ctrl+K");
  });
});
