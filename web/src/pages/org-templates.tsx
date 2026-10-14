import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { LayoutTemplate, Plus } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { AppIcon } from "@/components/app-icon";
import { EmptyState } from "@/components/empty-state";
import { InlineError } from "@/components/inline-error";
import { PageHeader } from "@/components/page-header";
import { useTemplateLooks } from "@/hooks/use-template-looks";
import { useTemplatesApi } from "@/hooks/use-templates-api";
import { NEW_TEMPLATE_PATH, templatePath } from "@/lib/routes";
import { usageLabel, type TemplateSummary } from "@/lib/templates";
import { relativeTime } from "@/lib/utils";

// OrgTemplatesPage is /templates: the organization's templates as cards
// (how many apps use each, when it last changed) and New template.
export default function OrgTemplatesPage() {
  const api = useTemplatesApi();
  const [list, setList] = useState<TemplateSummary[] | null>(null);
  const [error, setError] = useState("");
  const looks = useTemplateLooks(api, list);

  useEffect(() => {
    api.listTemplates().then(setList, (e: unknown) => setError(e instanceof Error ? e.message : "Failed to load templates"));
  }, [api]);

  return (
    <div className="space-y-6">
      <PageHeader
        title="Templates"
        subtitle="Define an app once, then add it to any server with its own settings."
        actions={
          <Button asChild>
            <Link to={NEW_TEMPLATE_PATH}>
              <Plus className="size-4" /> New template
            </Link>
          </Button>
        }
      />
      {error ? (
        <InlineError>{error}</InlineError>
      ) : list === null ? (
        <div className="flex h-24 items-center justify-center">
          <Spinner />
        </div>
      ) : list.length === 0 ? (
        <EmptyState
          icon={LayoutTemplate}
          title="No templates yet. Create one here, or open an app's Settings and save it as a template."
        />
      ) : (
        <ul aria-label="Templates" className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
          {list.map((t) => (
            <li key={t.id}>
              <Link
                to={templatePath(t.id)}
                className="flex h-full items-start gap-3 rounded-xl border bg-card p-4 transition-colors duration-150 hover:bg-muted/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring motion-reduce:transition-none"
              >
                <AppIcon name={t.name} icon={looks[t.id]?.icon} color={looks[t.id]?.color} className="size-10 shrink-0 text-base" />
                <div className="min-w-0 space-y-1">
                  <p className="truncate text-[15px] font-semibold">{t.name}</p>
                  <p className="text-xs text-muted-foreground">{usageLabel(t)}</p>
                  <p className="text-xs text-muted-foreground">
                    <time dateTime={t.updated_at}>Updated {relativeTime(new Date(t.updated_at))}</time>
                  </p>
                </div>
              </Link>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
