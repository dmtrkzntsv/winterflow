import { useEffect, useRef, useState } from "react";

import type { TemplateSummary, TemplatesApi } from "@/lib/templates";

export type TemplateLook = { icon: string; color: string; summary: string };

// The list response has no org template body. Read each latest org revision
// once per mounted client for its look and searchable summary. Catalog cards
// read current catalog metadata from the list and need no revision request.
export function useTemplateLooks(
  api: Pick<TemplatesApi, "getRevision">,
  list: readonly Pick<TemplateSummary, "id" | "latest_revision" | "source">[] | null,
): Record<string, TemplateLook> {
  const [looks, setLooks] = useState<Record<string, TemplateLook>>({});
  const cache = useRef(new Map<string, TemplateLook>());
  const key = (list ?? [])
    .filter((t) => t.source === "org")
    .map((t) => `${t.id}@${t.latest_revision}`)
    .join(",");

  useEffect(() => {
    const entries = (list ?? []).filter((t) => t.source === "org");
    let live = true;
    // At most four reads run at once, so a large org does not occupy all of
    // the browser's connections and delay the selected template's form.
    let cursor = 0;
    const next: Record<string, TemplateLook> = {};
    for (const t of entries) {
      const found = cache.current.get(`${t.id}@${t.latest_revision}`);
      if (found) next[t.id] = found;
    }
    setLooks(next);
    const read = async () => {
      while (live && cursor < entries.length) {
        const t = entries[cursor++];
        const version = `${t.id}@${t.latest_revision}`;
        if (cache.current.has(version)) continue;
        try {
          const v = await api.getRevision(t.id, t.latest_revision);
          const look = { icon: v.body.icon, color: v.body.color, summary: v.body.summary };
          cache.current.set(version, look);
          if (live) setLooks((prev) => ({ ...prev, [t.id]: look }));
        } catch {
          // Name and initial tile remain useful when one revision fails.
        }
      }
    };
    for (let i = 0; i < Math.min(4, entries.length); i++) void read();
    return () => {
      live = false;
    };
    // key contains precisely the revision reads this hook needs.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [api, key]);

  return looks;
}
