import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import {
  THEME_KEY,
  getThemePreference,
  resolveTheme,
  setThemePreference,
  startTheme,
  subscribeThemePreference,
} from "./theme"

type Listener = () => void

function mockMatchMedia(initial: boolean) {
  const listeners = new Set<Listener>()
  const mql = {
    matches: initial,
    addEventListener: (_: string, l: Listener) => listeners.add(l),
    removeEventListener: (_: string, l: Listener) => listeners.delete(l),
  }
  vi.stubGlobal("matchMedia", vi.fn(() => mql))
  return {
    set(matches: boolean) {
      mql.matches = matches
      listeners.forEach((l) => l())
    },
    listeners,
  }
}

const dark = () => document.documentElement.classList.contains("dark")

describe("theme preference", () => {
  let stop: () => void = () => {}

  beforeEach(() => localStorage.clear())
  afterEach(() => {
    stop()
    stop = () => {}
    vi.unstubAllGlobals()
    document.documentElement.classList.remove("dark")
    localStorage.clear()
  })

  it("follows the system by default and tracks its changes", () => {
    const media = mockMatchMedia(true)
    stop = startTheme()
    expect(getThemePreference()).toBe("system")
    expect(dark()).toBe(true)

    media.set(false)
    expect(dark()).toBe(false)
  })

  it("keeps an explicit choice over the system and stores it", () => {
    const media = mockMatchMedia(true)
    stop = startTheme()
    setThemePreference("light")
    expect(dark()).toBe(false)
    expect(localStorage.getItem(THEME_KEY)).toBe("light")

    media.set(true)
    expect(dark()).toBe(false)
  })

  it("reads a stored choice at start", () => {
    mockMatchMedia(false)
    localStorage.setItem(THEME_KEY, "dark")
    stop = startTheme()
    expect(getThemePreference()).toBe("dark")
    expect(dark()).toBe(true)
  })

  it("forgets the stored value when going back to the system", () => {
    mockMatchMedia(true)
    localStorage.setItem(THEME_KEY, "light")
    stop = startTheme()
    setThemePreference("system")
    expect(localStorage.getItem(THEME_KEY)).toBeNull()
    expect(dark()).toBe(true)
  })

  it("reads a stored value it doesn't know as the system", () => {
    mockMatchMedia(false)
    localStorage.setItem(THEME_KEY, "purple")
    stop = startTheme()
    expect(getThemePreference()).toBe("system")
  })

  it("picks up a choice made in another tab and tells subscribers", () => {
    mockMatchMedia(false)
    stop = startTheme()
    const seen = vi.fn()
    const unsubscribe = subscribeThemePreference(seen)
    window.dispatchEvent(new StorageEvent("storage", { key: THEME_KEY, newValue: "dark" }))
    expect(getThemePreference()).toBe("dark")
    expect(dark()).toBe(true)
    expect(seen).toHaveBeenCalled()
    unsubscribe()
  })

  it("follows localStorage.clear() in another tab (a storage event without a key)", () => {
    mockMatchMedia(false)
    localStorage.setItem(THEME_KEY, "dark")
    stop = startTheme()
    expect(getThemePreference()).toBe("dark")
    localStorage.clear()
    const seen = vi.fn()
    const unsubscribe = subscribeThemePreference(seen)
    window.dispatchEvent(new StorageEvent("storage", { key: null }))
    expect(getThemePreference()).toBe("system")
    expect(dark()).toBe(false)
    expect(seen).toHaveBeenCalled()
    unsubscribe()
  })

  it("tells subscribers about a change made here", () => {
    mockMatchMedia(false)
    stop = startTheme()
    const seen = vi.fn()
    const unsubscribe = subscribeThemePreference(seen)
    setThemePreference("dark")
    expect(seen).toHaveBeenCalledTimes(1)
    unsubscribe()
    setThemePreference("light")
    expect(seen).toHaveBeenCalledTimes(1)
  })

  it("stops listening when stopped", () => {
    const media = mockMatchMedia(false)
    startTheme()()
    expect(media.listeners.size).toBe(0)
  })

  it("is light without matchMedia", () => {
    stop = startTheme()
    expect(dark()).toBe(false)
  })
})

describe("resolveTheme", () => {
  it("resolves the system preference from the system", () => {
    expect(resolveTheme("system", true)).toBe("dark")
    expect(resolveTheme("system", false)).toBe("light")
    expect(resolveTheme("light", true)).toBe("light")
    expect(resolveTheme("dark", false)).toBe("dark")
  })
})
