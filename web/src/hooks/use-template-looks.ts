import { useEffect, useState } from "react";

import type { TemplateSummary, TemplatesApi } from "@/lib/templates";

export type TemplateLook = { icon: string; color: string };

// useTemplateLooks are the icon and colour of each listed template, by id:
// read from its latest revision (the list carries neither; a revision never
// changes, so each is read once per client). A template whose read fails
// keeps the letter tile.
export function useTemplateLooks(
  api: Pick<TemplatesApi, "getRevision">,
  list: readonly Pick<TemplateSummary, "id" | "latest_revision">[] | null,
): Record<string, TemplateLook> {
  const [looks, setLooks] = useState<Record<string, TemplateLook>>({});
  const key = (list ?? []).map((t) => `${t.id}@${t.latest_revision}`).join(",");

  useEffect(() => {
    if (!key) return;
    let live = true;
    for (const entry of key.split(",")) {
      const at = entry.lastIndexOf("@");
      const id = entry.slice(0, at);
      api.getRevision(id, Number(entry.slice(at + 1))).then(
        (v) => {
          if (live && (v.body.icon || v.body.color)) setLooks((prev) => ({ ...prev, [id]: { icon: v.body.icon, color: v.body.color } }));
        },
        () => {},
      );
    }
    return () => {
      live = false;
    };
  }, [api, key]);

  return looks;
}
