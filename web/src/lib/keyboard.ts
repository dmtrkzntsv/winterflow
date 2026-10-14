export type KeyLike = Pick<KeyboardEvent, "key" | "metaKey" | "ctrlKey" | "altKey" | "shiftKey">;

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
// text fields included.
export function isPaletteShortcut(e: KeyLike): boolean {
  return commandOnly(e) && e.key.toLowerCase() === "k";
}

// serverShortcutIndex maps ⌘1…⌘9 (Ctrl elsewhere) to the index of the
// server to switch to, null for any other key.
export function serverShortcutIndex(e: KeyLike): number | null {
  if (!commandOnly(e) || !/^[1-9]$/.test(e.key)) return null;
  return Number(e.key) - 1;
}
