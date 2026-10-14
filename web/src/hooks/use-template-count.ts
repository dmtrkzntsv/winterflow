import { useEffect, useRef, useState } from "react";

import { useTemplatesApi } from "@/hooks/use-templates-api";

// useTemplateCount is how many templates the organization has, read once
// (the first time enabled is true) and kept for as long as the caller lives,
// so reopening a dialog doesn't ask again. null while unknown; a failed read
// counts as none.
export function useTemplateCount(enabled = true): number | null {
  const api = useTemplatesApi();
  const [count, setCount] = useState<number | null>(null);
  const started = useRef(false);
  useEffect(() => {
    if (!enabled || started.current) return;
    started.current = true;
    api.listTemplates().then(
      (list) => setCount(list.length),
      () => setCount(0),
    );
  }, [enabled, api]);
  return count;
}
