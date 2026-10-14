import { useState, type ReactNode } from "react";

import {
  HeadingVisibleContext,
  PageTitleContext,
  SetHeadingVisibleContext,
  SetPageTitleContext,
} from "@/layouts/page-title-base";

// PageTitleProvider holds the title the current page set with usePageTitle,
// and whether that page's own H1 is on screen (useHeadingInView).
export function PageTitleProvider({ children }: { children: ReactNode }) {
  const [title, setTitle] = useState("");
  const [headingVisible, setHeadingVisible] = useState(false);
  return (
    <SetPageTitleContext.Provider value={setTitle}>
      <PageTitleContext.Provider value={title}>
        <SetHeadingVisibleContext.Provider value={setHeadingVisible}>
          <HeadingVisibleContext.Provider value={headingVisible}>{children}</HeadingVisibleContext.Provider>
        </SetHeadingVisibleContext.Provider>
      </PageTitleContext.Provider>
    </SetPageTitleContext.Provider>
  );
}
