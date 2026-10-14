import { useMediaQuery } from "@/hooks/use-media-query"

// Below this width the shell is the phone/tablet one: the sidebar is a
// sheet behind a slim top bar (the vendored sidebar reads useIsMobile).
export const MOBILE_BREAKPOINT = 1024

export function useIsMobile(): boolean {
  return useMediaQuery(`(max-width: ${MOBILE_BREAKPOINT - 1}px)`)
}
