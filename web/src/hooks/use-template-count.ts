import { useEffect, useRef, useState } from "react";

import { useTemplatesApi } from "@/hooks/use-templates-api";

// useTemplateCount is how many templates of its own the organization has
// (what New app → From a template offers), read once
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
      (list) => setCount(list.filter((t) => t.source === "org" || (t.source === "catalog" && !t.withdrawn_at)).length),
      () => setCount(0),
    );
  }, [enabled, api]);
  return count;
}
