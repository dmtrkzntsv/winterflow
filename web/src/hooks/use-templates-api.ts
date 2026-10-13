import { useMemo } from "react";

import { useNotifications } from "@/context/use-notifications";
import { createTemplatesApi, type TemplatesApi } from "@/lib/templates";

// useTemplatesApi is the templates client bound to this tab's SSE stream
// (instance commands wait for their result on it).
export function useTemplatesApi(): TemplatesApi {
  const { waitFor } = useNotifications();
  return useMemo(() => createTemplatesApi(waitFor), [waitFor]);
}
