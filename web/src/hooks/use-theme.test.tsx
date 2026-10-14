import { act, render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, describe, expect, it } from "vitest"

import { useThemePreference } from "@/hooks/use-theme"
import { THEME_KEY, setThemePreference } from "@/lib/theme"

function Probe() {
  const [theme, setTheme] = useThemePreference()
  return (
    <div>
      <span data-testid="theme">{theme}</span>
      <button onClick={() => setTheme("dark")}>dark</button>
    </div>
  )
}

describe("useThemePreference", () => {
  afterEach(() => {
    act(() => setThemePreference("system"))
    localStorage.clear()
    document.documentElement.classList.remove("dark")
  })

  it("reads and changes the preference", async () => {
    render(<Probe />)
    expect(screen.getByTestId("theme")).toHaveTextContent("system")
    await userEvent.setup().click(screen.getByRole("button", { name: "dark" }))
    expect(screen.getByTestId("theme")).toHaveTextContent("dark")
    expect(localStorage.getItem(THEME_KEY)).toBe("dark")
    expect(document.documentElement).toHaveClass("dark")
  })

  it("re-renders on a change made elsewhere", () => {
    render(<Probe />)
    act(() => setThemePreference("light"))
    expect(screen.getByTestId("theme")).toHaveTextContent("light")
  })
})
