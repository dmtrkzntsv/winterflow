import { useSyncExternalStore } from "react";

import { isDarkMode, subscribeDarkMode } from "@/lib/theme";

// useDarkMode follows the `.dark` class on <html> (set by followSystemTheme),
// for the few widgets that need a JS-side theme, such as the code editor.
export function useDarkMode(): boolean {
  return useSyncExternalStore(subscribeDarkMode, isDarkMode, () => false);
}
