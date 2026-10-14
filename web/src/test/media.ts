import { vi } from "vitest";

// stubViewport makes window.matchMedia answer (min-width: Npx) and
// (max-width: Npx) queries for a viewport `width` px wide, and
// prefers-color-scheme: dark when opts.dark is set. jsdom has no
// matchMedia at all (every hook reads that as a desktop). Undo with
// vi.unstubAllGlobals().
export function stubViewport(width: number, opts: { dark?: boolean } = {}): void {
  vi.stubGlobal("matchMedia", (query: string) => {
    const min = /\(min-width:\s*(\d+)px\)/.exec(query);
    const max = /\(max-width:\s*(\d+)px\)/.exec(query);
    let matches = false;
    if (min) matches = width >= Number(min[1]);
    else if (max) matches = width <= Number(max[1]);
    else if (query.includes("prefers-color-scheme: dark")) matches = Boolean(opts.dark);
    return {
      matches,
      media: query,
      onchange: null,
      addEventListener: () => {},
      removeEventListener: () => {},
      addListener: () => {},
      removeListener: () => {},
      dispatchEvent: () => false,
    };
  });
}
