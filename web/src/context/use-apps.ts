import { useContext } from "react";

import { AppsContext } from "./apps-context-base";

export function useApps() {
  const context = useContext(AppsContext);
  if (!context) {
    throw new Error("useApps must be used within an AppsProvider");
  }
  return context;
}
