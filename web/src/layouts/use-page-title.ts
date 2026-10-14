import { useContext, useEffect, useRef, type RefObject } from "react";

import {
  APP_NAME,
  HeadingVisibleContext,
  PageTitleContext,
  SetHeadingVisibleContext,
  SetPageTitleContext,
} from "@/layouts/page-title-base";

// The phone top bar is 3.5rem (h-14) and sticks over the page: a heading
// behind it is not in view.
const TOP_BAR_PX = 56;

// usePageTitle names the current page: the shell's mobile top bar shows it
// and the browser tab reads "<title> · WinterFlow". The page that unmounts
// clears it, so a page that names nothing never shows the last one's name.
export function usePageTitle(title: string): void {
  const setTitle = useContext(SetPageTitleContext);
  useEffect(() => {
    setTitle?.(title);
    const previous = document.title;
    document.title = title ? `${title} · ${APP_NAME}` : APP_NAME;
    return () => {
      setTitle?.("");
      document.title = previous;
    };
  }, [setTitle, title]);
}

// useCurrentPageTitle is the title the current page set ("" when none).
export function useCurrentPageTitle(): string {
  return useContext(PageTitleContext);
}

// useHeadingInView watches the page's own H1 (put the returned ref on it):
// the shell's phone top bar repeats the title only once the H1 has scrolled
// out of view. Without IntersectionObserver the heading counts as not in
// view, so the bar keeps the title.
export function useHeadingInView<T extends Element>(): RefObject<T | null> {
  const ref = useRef<T>(null);
  const setVisible = useContext(SetHeadingVisibleContext);
  useEffect(() => {
    const el = ref.current;
    if (!el || !setVisible || typeof IntersectionObserver === "undefined") return;
    const observer = new IntersectionObserver(
      (entries) => {
        const last = entries[entries.length - 1];
        if (last) setVisible(last.isIntersecting);
      },
      { rootMargin: `-${TOP_BAR_PX}px 0px 0px 0px` },
    );
    observer.observe(el);
    return () => {
      observer.disconnect();
      setVisible(false);
    };
  }, [setVisible]);
  return ref;
}

// useTopBarTitle is what the phone top bar shows: the page's title, or
// nothing while the page's own H1 is in view (it would only repeat it).
export function useTopBarTitle(): string {
  const title = useContext(PageTitleContext);
  const headingVisible = useContext(HeadingVisibleContext);
  return headingVisible ? "" : title;
}
