import { useState, type ReactNode } from "react";

import { PageTitleContext, SetPageTitleContext } from "@/layouts/page-title-base";

// PageTitleProvider holds the title the current page set with usePageTitle.
export function PageTitleProvider({ children }: { children: ReactNode }) {
  const [title, setTitle] = useState("");
  return (
    <SetPageTitleContext.Provider value={setTitle}>
      <PageTitleContext.Provider value={title}>{children}</PageTitleContext.Provider>
    </SetPageTitleContext.Provider>
  );
}
