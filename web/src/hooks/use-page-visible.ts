import { useSyncExternalStore } from "react";

function subscribe(onChange: () => void) {
  document.addEventListener("visibilitychange", onChange);
  return () => document.removeEventListener("visibilitychange", onChange);
}

// usePageVisible reports whether the page is visible (not a background
// tab, not a minimized window).
export function usePageVisible(): boolean {
  return useSyncExternalStore(subscribe, () => document.visibilityState === "visible", () => true);
}
