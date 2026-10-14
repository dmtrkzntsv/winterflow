import { useSyncExternalStore } from "react"

import {
  getThemePreference,
  setThemePreference,
  subscribeThemePreference,
  type ThemePreference,
} from "@/lib/theme"

const serverSnapshot = (): ThemePreference => "system"

// useThemePreference is the user's Light/Dark/System choice and its setter;
// every caller re-renders when it changes (here or in another tab).
export function useThemePreference(): [ThemePreference, (preference: ThemePreference) => void] {
  const preference = useSyncExternalStore(subscribeThemePreference, getThemePreference, serverSnapshot)
  return [preference, setThemePreference]
}
