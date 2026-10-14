// IMAGE_LINE finds `image:` lines in a compose file (global: matchAll only).
const IMAGE_LINE = /^\s*image:\s*["']?([\w][\w./:@-]*)/gm;

// imageRefs are the image references a compose file names, each once, in
// file order.
export function imageRefs(content: string): string[] {
  return Array.from(new Set(Array.from(content.matchAll(IMAGE_LINE), (m) => m[1]).filter(Boolean)));
}
