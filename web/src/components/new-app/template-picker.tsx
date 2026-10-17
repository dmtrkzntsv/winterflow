import { useState } from "react";

import { CatalogLogo } from "@/components/catalog-logo";
import { Input } from "@/components/ui/input";
import type { TemplateLook } from "@/hooks/use-template-looks";
import { templateSearch } from "@/lib/template-search";
import type { TemplateSummary } from "@/lib/templates";

export function TemplatePicker({
  templates,
  looks,
  onPick,
}: {
  templates: TemplateSummary[];
  looks: Record<string, TemplateLook>;
  onPick: (template: TemplateSummary) => void;
}) {
  const [query, setQuery] = useState("");
  const found = templateSearch(templates, query, looks);
  const org = found.filter((t) => t.source === "org");
  const catalog = found.filter((t) => t.source === "catalog");
  const cards = (items: TemplateSummary[]) => (
    <div className="grid gap-3 sm:grid-cols-2">
      {items.map((t) => (
        <button
          key={t.id}
          type="button"
          onClick={() => onPick(t)}
          className="flex min-h-20 items-start gap-3 rounded-xl border bg-card p-3 text-left transition-colors duration-150 hover:bg-muted/50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring motion-reduce:transition-none"
        >
          <CatalogLogo template={t} icon={looks[t.id]?.icon} color={looks[t.id]?.color} className="size-11 rounded-md" />
          <span className="min-w-0 space-y-1">
            <span className="block truncate font-medium">{t.name}</span>
            <span className="block text-sm text-muted-foreground">{t.source === "catalog" ? t.catalog?.summary : looks[t.id]?.summary}</span>
          </span>
        </button>
      ))}
    </div>
  );

  return (
    <div className="space-y-6">
      <Input
        type="search"
        aria-label="Search templates"
        placeholder="Search templates"
        value={query}
        onChange={(e) => setQuery(e.target.value)}
        autoFocus
      />
      {found.length === 0 ? <p className="text-sm text-muted-foreground">No templates match “{query.trim()}”.</p> : null}
      {org.length > 0 ? (
        <section aria-label="Your templates" className="space-y-3">
          <h2 className="text-sm font-semibold">Your templates</h2>
          {cards(org)}
        </section>
      ) : null}
      {catalog.length > 0 ? (
        <section aria-label="Catalog" className="space-y-3">
          <h2 className="text-sm font-semibold">Catalog</h2>
          {cards(catalog)}
        </section>
      ) : null}
    </div>
  );
}
