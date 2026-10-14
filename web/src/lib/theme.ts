/**
 * Light, dark, or the system's: the user's choice lives in localStorage
 * (THEME_KEY; absent = follow the system) and shows as shadcn's `.dark`
 * class on <html>. index.html applies it before the first paint; startTheme
 * keeps it in step with the system and with other tabs.
 */
export type ThemePreference = "light" | "dark" | "system"

export const THEME_KEY = "wf.theme"

const listeners = new Set<() => void>()
// current is the preference in force; null until first read.
let current: ThemePreference | null = null

function parse(value: string | null): ThemePreference {
  return value === "light" || value === "dark" ? value : "system"
}

function readStored(): ThemePreference {
  try {
    return parse(window.localStorage.getItem(THEME_KEY))
  } catch {
    return "system" // storage blocked: follow the system
  }
}

function systemIsDark(): boolean {
  return (
    typeof window.matchMedia === "function" &&
    window.matchMedia("(prefers-color-scheme: dark)").matches
  )
}

/** resolveTheme is the theme a preference shows. */
export function resolveTheme(
  preference: ThemePreference,
  systemDark: boolean,
): "light" | "dark" {
  if (preference === "system") return systemDark ? "dark" : "light"
  return preference
}

function apply(): void {
  const theme = resolveTheme(getThemePreference(), systemIsDark())
  document.documentElement.classList.toggle("dark", theme === "dark")
}

function emit(): void {
  listeners.forEach((l) => l())
}

/** getThemePreference is the preference in force (a useSyncExternalStore snapshot). */
export function getThemePreference(): ThemePreference {
  current ??= readStored()
  return current
}

/** setThemePreference stores and applies a choice; "system" forgets the stored one. */
export function setThemePreference(preference: ThemePreference): void {
  current = preference
  try {
    if (preference === "system") window.localStorage.removeItem(THEME_KEY)
    else window.localStorage.setItem(THEME_KEY, preference)
  } catch {
    // Storage blocked: the choice holds for this page only.
  }
  apply()
  emit()
}

/** subscribeThemePreference calls onChange after every change, here or in another tab. */
export function subscribeThemePreference(onChange: () => void): () => void {
  listeners.add(onChange)
  return () => {
    listeners.delete(onChange)
  }
}

/**
 * startTheme applies the stored preference and keeps it applied: a system
 * change matters while the preference is "system", and a choice made in
 * another tab arrives as a storage event. Returns a function that stops
 * (used by tests).
 */
export function startTheme(): () => void {
  current = readStored()
  apply()
  const media =
    typeof window.matchMedia === "function"
      ? window.matchMedia("(prefers-color-scheme: dark)")
      : null
  const onSystem = () => {
    if (getThemePreference() === "system") apply()
  }
  const onStorage = (event: StorageEvent) => {
    // key === null: localStorage.clear() in another tab.
    if (event.key !== null && event.key !== THEME_KEY) return
    current = event.key === null ? "system" : parse(event.newValue)
    apply()
    emit()
  }
  media?.addEventListener("change", onSystem)
  window.addEventListener("storage", onStorage)
  return () => {
    media?.removeEventListener("change", onSystem)
    window.removeEventListener("storage", onStorage)
  }
}

/** Whether the page is currently in dark mode (`.dark` on <html>). */
export function isDarkMode(): boolean {
  return document.documentElement.classList.contains("dark")
}

/**
 * Calls onChange whenever `.dark` on <html> may have flipped. Returns the
 * unsubscribe function (a useSyncExternalStore subscriber).
 */
export function subscribeDarkMode(onChange: () => void): () => void {
  const observer = new MutationObserver(onChange)
  observer.observe(document.documentElement, {
    attributes: true,
    attributeFilter: ["class"],
  })
  return () => observer.disconnect()
}
