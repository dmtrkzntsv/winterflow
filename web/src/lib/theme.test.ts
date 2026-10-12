import { afterEach, describe, expect, it, vi } from "vitest"

import { followSystemTheme } from "./theme"

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

describe("followSystemTheme", () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    document.documentElement.classList.remove("dark")
  })

  it("sets .dark from prefers-color-scheme and tracks changes", () => {
    const media = mockMatchMedia(true)
    const stop = followSystemTheme()
    expect(document.documentElement.classList.contains("dark")).toBe(true)

    media.set(false)
    expect(document.documentElement.classList.contains("dark")).toBe(false)

    stop()
    expect(media.listeners.size).toBe(0)
  })
})
