import type { TemplateLook } from "@/hooks/use-template-looks";
import type { TemplateSummary } from "@/lib/templates";

// templateSearch applies one query to org and catalog templates while
// preserving their order. Catalog identity comes from live catalog metadata;
// an org template's summary comes from its latest revision.
export function templateSearch(
  list: readonly TemplateSummary[],
  query: string,
  orgDetails: Record<string, TemplateLook> = {},
): TemplateSummary[] {
  const needle = query.trim().toLocaleLowerCase();
  if (!needle) return [...list];
  return list.filter((t) => {
    const fields = t.source === "catalog"
      ? [t.name, t.catalog?.summary, ...(t.catalog?.keywords ?? []), ...(t.catalog?.alternative_to ?? [])]
      : [t.name, orgDetails[t.id]?.summary];
    return fields.some((field) => field?.toLocaleLowerCase().includes(needle));
  });
}
