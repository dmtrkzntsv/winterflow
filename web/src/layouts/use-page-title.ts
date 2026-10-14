import { useContext, useEffect } from "react";

import { APP_NAME, PageTitleContext, SetPageTitleContext } from "@/layouts/page-title-base";

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
