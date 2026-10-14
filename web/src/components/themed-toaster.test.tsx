import { act, render, screen } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"

import { ThemedToaster } from "@/components/themed-toaster"

vi.mock("@/components/ui/sonner", () => ({
  Toaster: ({ theme }: { theme?: string }) => <p>toaster {theme}</p>,
}))

describe("ThemedToaster", () => {
  afterEach(() => document.documentElement.classList.remove("dark"))

  it("follows the theme on screen", async () => {
    render(<ThemedToaster />)
    expect(screen.getByText("toaster light")).toBeInTheDocument()
    act(() => document.documentElement.classList.add("dark"))
    expect(await screen.findByText("toaster dark")).toBeInTheDocument()
  })
})
