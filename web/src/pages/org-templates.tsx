import { useEffect, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { Plus } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Spinner } from "@/components/ui/spinner";
import { InlineError } from "@/components/inline-error";
import { useTemplatesApi } from "@/hooks/use-templates-api";
import { useAppBreadcrumbs } from "@/layouts/use-app-layout";
import { instancesLabel, type TemplateSummary } from "@/lib/templates";
import { relativeTime } from "@/lib/utils";

const BREADCRUMBS = [{ label: "Organization" }, { label: "Templates" }];

// OrgTemplatesPage is Org → Templates: the org's stack definitions, each
// with its latest revision and how many apps run it.
export default function OrgTemplatesPage() {
  useAppBreadcrumbs(BREADCRUMBS);
  const api = useTemplatesApi();
  const navigate = useNavigate();
  const [list, setList] = useState<TemplateSummary[] | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    api.listTemplates().then(setList, (e: unknown) => setError(e instanceof Error ? e.message : "Failed to load templates"));
  }, [api]);

  return (
    <Card>
      <CardHeader className="flex-row flex-wrap items-start justify-between gap-x-4 gap-y-3 space-y-0">
        <div className="min-w-0 space-y-1.5">
          <CardTitle>Templates</CardTitle>
          <CardDescription>
            A stack defined once, added as an app on any server with its own values. Apps added from a template
            keep its files until you unlink them.
          </CardDescription>
        </div>
        <Button size="sm" onClick={() => navigate("/org/templates/new")}>
          <Plus className="size-4" /> Add template
        </Button>
      </CardHeader>
      <CardContent>
        {error ? (
          <InlineError>{error}</InlineError>
        ) : list === null ? (
          <div className="flex h-24 items-center justify-center">
            <Spinner />
          </div>
        ) : list.length === 0 ? (
          <p className="rounded-md border border-dashed p-6 text-center text-sm text-muted-foreground">
            No templates yet. Add one here, or open an app's Settings and save it as a template.
          </p>
        ) : (
          <ul className="divide-y rounded-md border">
            {list.map((t) => (
              <li key={t.id} className="flex flex-wrap items-center gap-x-4 gap-y-1 px-4 py-3 text-sm">
                <Link to={`/org/templates/${t.id}`} className="font-medium hover:underline">
                  {t.name}
                </Link>
                <span className="text-muted-foreground">r{t.latest_revision}</span>
                <span className="text-muted-foreground">{instancesLabel(t)}</span>
                <time className="ml-auto text-xs text-muted-foreground" dateTime={t.updated_at}>
                  {relativeTime(new Date(t.updated_at))}
                </time>
              </li>
            ))}
          </ul>
        )}
      </CardContent>
    </Card>
  );
}
