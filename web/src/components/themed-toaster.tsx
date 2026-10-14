import { Toaster } from "@/components/ui/sonner"
import { useDarkMode } from "@/hooks/use-dark-mode"

// ThemedToaster is the app's Toaster in the theme on screen: the vendored
// Toaster asks next-themes, which this app doesn't use, so the theme is
// passed in (its props win over its own guess).
export function ThemedToaster() {
  return <Toaster theme={useDarkMode() ? "dark" : "light"} />
}
