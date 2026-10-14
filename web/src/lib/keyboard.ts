export type KeyLike = Pick<KeyboardEvent, "key" | "metaKey" | "ctrlKey" | "altKey" | "shiftKey"> &
  Partial<Pick<KeyboardEvent, "code" | "repeat">>;

// isMacPlatform decides whether shortcuts read ⌘ or Ctrl.
export function isMacPlatform(): boolean {
  if (typeof navigator === "undefined") return false;
  return /Mac|iPhone|iPad|iPod/i.test(navigator.platform || navigator.userAgent);
}

export function modKeyLabel(): string {
  return isMacPlatform() ? "⌘" : "Ctrl+";
}

// shortcutLabel spells ⌘/Ctrl plus a key ("⌘K", "Ctrl+1").
export function shortcutLabel(key: string | number): string {
  return `${modKeyLabel()}${key}`;
}

function commandOnly(e: KeyLike): boolean {
  return (e.metaKey || e.ctrlKey) && !e.altKey && !e.shiftKey;
}

// isPaletteShortcut is ⌘K / Ctrl+K: it opens the palette from anywhere,
// text fields included. The physical key counts too (e.code), so it works on
// non-Latin layouts, and a held key doesn't toggle the palette back and forth.
export function isPaletteShortcut(e: KeyLike): boolean {
  return !e.repeat && commandOnly(e) && (e.code === "KeyK" || e.key.toLowerCase() === "k");
}

// serverShortcutIndex maps ⌘1…⌘9 (Ctrl elsewhere) to the index of the
// server to switch to, null for any other key (and for a held key). The
// physical key counts (e.code), so it works on layouts whose digit row types
// symbols.
export function serverShortcutIndex(e: KeyLike): number | null {
  if (e.repeat || !commandOnly(e)) return null;
  const digit = /^Digit([1-9])$/.exec(e.code ?? "")?.[1] ?? (/^[1-9]$/.test(e.key) ? e.key : null);
  return digit === null ? null : Number(digit) - 1;
}
