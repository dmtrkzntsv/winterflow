import { useCallback, useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { LayoutTemplate, Plus, Search } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Spinner } from "@/components/ui/spinner";
import { CatalogLogo } from "@/components/catalog-logo";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { EmptyState } from "@/components/empty-state";
import { InlineError } from "@/components/inline-error";
import { PageHeader } from "@/components/page-header";
import { RolloutStrip } from "@/components/rollout-strip";
import { useTemplateLooks } from "@/hooks/use-template-looks";
import { useRollout } from "@/hooks/use-rollout";
import { useRolloutsApi } from "@/hooks/use-rollouts-api";
import { useTemplatesApi } from "@/hooks/use-templates-api";
import { NEW_TEMPLATE_PATH, templatePath } from "@/lib/routes";
import { isActive } from "@/lib/rollouts";
import { templateSearch } from "@/lib/template-search";
import { usageLabel, type TemplateSummary } from "@/lib/templates";
import { relativeTime } from "@/lib/utils";

function CatalogCard({ template, used = false, onProgress }: {
  template: TemplateSummary;
  used?: boolean;
  onProgress: () => void;
}) {
  const rolloutsApi = useRolloutsApi();
  const { rollout, set, stop } = useRollout(rolloutsApi, "template_upgrade", used ? template.id : null);
  const [confirm, setConfirm] = useState(false);
  const progress = rollout ? `${rollout.id}:${rollout.done}:${rollout.skipped}:${rollout.state}` : "";
  const lastProgress = useRef("");
  useEffect(() => {
    const previous = lastProgress.current;
    lastProgress.current = progress;
    if (previous && progress && previous !== progress) onProgress();
  }, [progress, onProgress]);

  const upgradeAll = async () => {
    try {
      set(await rolloutsApi.upgradeAll(template.id, template.latest_revision));
    } catch (e) {
      toast.error("Couldn't start the update", { description: e instanceof Error ? e.message : undefined });
      throw e;
    }
  };

  return (
    <li className="relative min-w-0 rounded-xl border bg-card p-4 transition-colors duration-150 hover:bg-muted/40 focus-within:ring-2 focus-within:ring-ring motion-reduce:transition-none">
      <Link to={templatePath(template.id)} aria-label={template.name} className="absolute inset-0 rounded-xl focus-visible:outline-none" />
      <div className="flex items-start gap-3">
        <CatalogLogo template={template} className="size-10 shrink-0 text-base" />
        <div className="min-w-0 flex-1 space-y-1">
          <p className="truncate text-[15px] font-semibold">{template.name}</p>
          {template.catalog?.summary ? <p className="text-sm text-muted-foreground">{template.catalog.summary}</p> : null}
          {used ? <p className="text-xs text-muted-foreground">{usageLabel(template)}</p> : null}
          {template.behind > 0 ? <p className="text-xs font-medium text-amber-700 dark:text-amber-400">Upgrade available</p> : null}
          {template.withdrawn_at ? <p className="text-xs text-muted-foreground">Withdrawn from the catalog</p> : null}
        </div>
      </div>
      {used && template.behind > 0 ? (
        <div className="relative z-10 mt-3 flex justify-end">
          <Button size="sm" variant="outline" disabled={!!rollout && isActive(rollout)} onClick={() => setConfirm(true)}>
            Upgrade all
          </Button>
        </div>
      ) : null}
      {used ? <div className="relative z-10 mt-3"><RolloutStrip rollout={rollout} serverName={() => "a server"} onStop={stop} /></div> : null}
      <ConfirmDialog
        open={confirm}
        onOpenChange={setConfirm}
        destructive={false}
        title={`Upgrade ${template.behind} ${template.behind === 1 ? "app" : "apps"} to version ${template.latest_revision}?`}
        description="Apps are updated one at a time. An app needing a new value is skipped; the first failure stops the rest."
        confirmLabel="Upgrade all"
        onConfirm={upgradeAll}
      />
    </li>
  );
}

// The index has one org section followed by the catalog. Catalog templates
// with instances stay visible even after withdrawal; the remaining grid is
// searchable and contains only active templates.
export default function OrgTemplatesPage() {
  const api = useTemplatesApi();
  const [list, setList] = useState<TemplateSummary[] | null>(null);
  const [error, setError] = useState("");
  const [query, setQuery] = useState("");
  const looks = useTemplateLooks(api, list);
  const refresh = useCallback(() => {
    api.listTemplates().then(setList, (e: unknown) => setError(e instanceof Error ? e.message : "Failed to load templates"));
  }, [api]);

  useEffect(() => { refresh(); }, [refresh]);

  const org = list?.filter((t) => t.source === "org") ?? [];
  const catalog = list?.filter((t) => t.source === "catalog") ?? [];
  const used = catalog.filter((t) => t.instances > 0);
  const available = catalog.filter((t) => t.instances === 0 && !t.withdrawn_at);
  const matches = templateSearch(available, query);

  return (
    <div className="space-y-8">
      <PageHeader
        title="Templates"
        subtitle="Define an app once, then add it to any server with its own settings."
        actions={<Button asChild><Link to={NEW_TEMPLATE_PATH}><Plus className="size-4" /> New template</Link></Button>}
      />
      {error ? <InlineError>{error}</InlineError> : list === null ? (
        <div className="flex h-24 items-center justify-center"><Spinner /></div>
      ) : list.length === 0 ? (
        <EmptyState icon={LayoutTemplate} title="No templates yet. Create one here, or open an app's Settings and save it as a template." />
      ) : (
        <>
          <section aria-labelledby="org-templates-heading" className="space-y-3">
            <h2 id="org-templates-heading" className="text-lg font-semibold">Your templates</h2>
            {org.length ? (
              <ul aria-label="Your templates" className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
                {org.map((t) => (
                  <li key={t.id}>
                    <Link to={templatePath(t.id)} className="flex h-full items-start gap-3 rounded-xl border bg-card p-4 transition-colors duration-150 hover:bg-muted/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring motion-reduce:transition-none">
                      <CatalogLogo template={t} icon={looks[t.id]?.icon} color={looks[t.id]?.color} className="size-10 shrink-0 text-base" />
                      <div className="min-w-0 space-y-1">
                        <p className="truncate text-[15px] font-semibold">{t.name}</p>
                        {looks[t.id]?.summary ? <p className="text-sm text-muted-foreground">{looks[t.id].summary}</p> : null}
                        <p className="text-xs text-muted-foreground">{usageLabel(t)}</p>
                        <p className="text-xs text-muted-foreground"><time dateTime={t.updated_at}>Updated {relativeTime(new Date(t.updated_at))}</time></p>
                      </div>
                    </Link>
                  </li>
                ))}
              </ul>
            ) : <p className="text-sm text-muted-foreground">No organization templates yet.</p>}
          </section>
          {catalog.length ? (
            <section aria-labelledby="catalog-heading" className="space-y-4">
              <h2 id="catalog-heading" className="text-lg font-semibold">Catalog</h2>
              {used.length ? (
                <div className="space-y-3">
                  <h3 className="text-sm font-medium">Used in your organization</h3>
                  <ul aria-label="Catalog templates in use" className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
                    {used.map((t) => <CatalogCard key={t.id} template={t} used onProgress={refresh} />)}
                  </ul>
                </div>
              ) : null}
              {available.length ? (
                <div className="space-y-3">
                  <div className="relative max-w-md">
                    <Search aria-hidden className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
                    <Input aria-label="Search catalog" placeholder="Search catalog" value={query} onChange={(e) => setQuery(e.target.value)} className="pl-9" />
                  </div>
                  {matches.length ? (
                    <ul aria-label="Catalog templates" className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
                      {matches.map((t) => <CatalogCard key={t.id} template={t} onProgress={refresh} />)}
                    </ul>
                  ) : <p className="text-sm text-muted-foreground">No catalog templates match your search.</p>}
                </div>
              ) : null}
            </section>
          ) : null}
        </>
      )}
    </div>
  );
}
