import { useCallback, useSyncExternalStore } from "react";

function hasMatchMedia(): boolean {
  return typeof window !== "undefined" && typeof window.matchMedia === "function";
}

// useMediaQuery reports whether a CSS media query matches, right from the
// first render (a layout that redirects or picks a dialog form must not see
// a wrong first answer), and re-renders when that changes. Without
// matchMedia (jsdom) nothing matches: tests are desktops by default.
export function useMediaQuery(query: string): boolean {
  const subscribe = useCallback(
    (onChange: () => void) => {
      if (!hasMatchMedia()) return () => {};
      const mql = window.matchMedia(query);
      mql.addEventListener("change", onChange);
      return () => mql.removeEventListener("change", onChange);
    },
    [query],
  );
  const snapshot = () => hasMatchMedia() && window.matchMedia(query).matches;
  return useSyncExternalStore(subscribe, snapshot, () => false);
}
