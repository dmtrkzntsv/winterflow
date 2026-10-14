import { useMediaQuery } from "@/hooks/use-media-query";

// useIsPhone: below Tailwind's sm breakpoint (640px). Without matchMedia
// (jsdom) it is false: tests are desktops by default.
export function useIsPhone(): boolean {
  return useMediaQuery("(max-width: 639px)");
}
