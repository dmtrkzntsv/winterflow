import { PHONE_QUERY } from "@/components/responsive-dialog";
import { useMediaQuery } from "@/hooks/use-media-query";

// useIsPhone: below Tailwind's sm breakpoint (640px), the same query the
// dialogs turn into bottom sheets on. Without matchMedia (jsdom) it is
// false: tests are desktops by default.
export function useIsPhone(): boolean {
  return useMediaQuery(PHONE_QUERY);
}
