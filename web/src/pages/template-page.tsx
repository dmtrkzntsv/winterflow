import { useCallback, useEffect, useMemo, useState } from "react";
import { useLocation, useNavigate, useParams, useSearchParams } from "react-router-dom";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { ConfirmButton } from "@/components/confirm-button";
import { StickyActionBar } from "@/components/editor-section";
import { InlineError } from "@/components/inline-error";
import { NotFound } from "@/components/not-found";
import { LoadError, PageSpinner } from "@/components/page-states";
import { RolloutStrip } from "@/components/rollout-strip";
import { TemplateEditor } from "@/components/template-editor";
import { TemplateInstances } from "@/components/template-instances";
import { TemplateRevisions } from "@/components/template-revisions";
import { useServers } from "@/context/use-servers";
import { useRollout } from "@/hooks/use-rollout";
import { useTemplatesApi } from "@/hooks/use-templates-api";
import { useAppBreadcrumbs } from "@/layouts/use-app-layout";
import { createRolloutsApi, isActive } from "@/lib/rollouts";
import { bodyFromDraft, draftFromBody, draftProblem, emptyDraft, type TemplateDraft } from "@/lib/template-draft";
import { ApiError, type InstanceView, type TemplateView } from "@/lib/templates";

const TABS = ["editor", "revisions", "instances"] as const;

// sameDraft says whether d still holds what base held. A field the editor
// didn't touch keeps its reference; only the others are compared by value
// (typing a change and taking it back is clean again).
function sameDraft(d: TemplateDraft, base: TemplateDraft): boolean {
  return (Object.keys(d) as (keyof TemplateDraft)[]).every(
    (k) => d[k] === base[k] || JSON.stringify(d[k]) === JSON.stringify(base[k]),
  );
}

// DraftState is what "Save as template" hands the new template's editor:
// the draft, what was left out of it, and the app it was built from.
export type DraftState = { draft: TemplateDraft; notes: string[]; appName: string };

// TemplatePage is one template (/org/templates/:templateId) or a new one
// (/org/templates/new, optionally prefilled by Save as template): its
// editor, where Save adds a revision, its revisions with their changes and
// the apps it runs as. The tab lives in ?tab=.
export default function TemplatePage() {
  const { templateId } = useParams();
  const location = useLocation();
  const navigate = useNavigate();
  const api = useTemplatesApi();
  const rolloutsApi = useMemo(() => createRolloutsApi(), []);
  const { servers } = useServers();
  const prefill = (location.state as DraftState | null) ?? null;
  const [params, setParams] = useSearchParams();
  const tab = (TABS as readonly string[]).includes(params.get("tab") ?? "") ? params.get("tab")! : "editor";

  const [view, setView] = useState<TemplateView | null>(null);
  const [instances, setInstances] = useState<InstanceView[]>([]);
  const [draft, setDraft] = useState<TemplateDraft>(() => prefill?.draft ?? emptyDraft());
  // baseline is the draft as loaded (null: a prefilled draft, unsaved).
  const [baseline, setBaseline] = useState<TemplateDraft | null>(() => (prefill ? null : draft));
  const [loadError, setLoadError] = useState<ApiError | Error | null>(null);
  const [note, setNote] = useState("");
  const [saving, setSaving] = useState(false);
  // tried: a save was refused in the browser, so the draft's problem shows
  // (and follows the edits) beside Save.
  const [tried, setTried] = useState(false);
  // conflict is the revision someone saved while this editor was open.
  const [conflict, setConflict] = useState<number | null>(null);

  const breadcrumbs = useMemo(
    () => [{ label: "Templates", href: "/org/templates" }, { label: view?.name ?? (templateId ? "Template" : "Add template") }],
    [view?.name, templateId],
  );
  useAppBreadcrumbs(breadcrumbs);

  // load reads the template into the editor, and its instances unless
  // only the template changed (a save).
  const load = useCallback(
    async (withInstances = true) => {
      if (!templateId) return;
      setLoadError(null);
      try {
        const [v, list] = await Promise.all([
          api.getTemplate(templateId),
          withInstances ? api.listInstances({ templateId }) : null,
        ]);
        const loaded = draftFromBody(v.name, v.body);
        setView(v);
        if (list) setInstances(list);
        setDraft(loaded);
        setBaseline(loaded);
        setConflict(null);
      } catch (e) {
        setLoadError(e instanceof Error ? e : new Error("Failed to load the template"));
      }
    },
    [api, templateId],
  );

  useEffect(() => {
    void load();
  }, [load]);

  // reloadInstances follows an upgrade without touching the editor.
  const reloadInstances = useCallback(() => {
    if (templateId) api.listInstances({ templateId }).then(setInstances, () => {});
  }, [api, templateId]);

  // The template's "Upgrade all" rollout: each step it takes (and its end)
  // moves an instance, so the list follows.
  const { rollout, set: setRollout, stop } = useRollout(rolloutsApi, "template_upgrade", templateId ?? null);
  const progress = rollout ? `${rollout.id}:${rollout.done}:${rollout.skipped}:${rollout.state}` : "";
  useEffect(() => {
    if (progress) reloadInstances();
  }, [progress, reloadInstances]);
  const serverName = (id: string) => servers.find((s) => s.id === id)?.name ?? id;

  const upgradeAll = async () => {
    if (!view) return;
    try {
      setRollout(await rolloutsApi.upgradeAll(view.id, view.latest_revision));
    } catch (e) {
      toast.error("Couldn't start the upgrade", { description: e instanceof Error ? e.message : undefined });
    }
  };

  const dirty = baseline === null || !sameDraft(draft, baseline);

  const problem = tried ? draftProblem(draft) : null;

  const save = async () => {
    if (draftProblem(draft)) {
      setTried(true);
      return;
    }
    setSaving(true);
    try {
      const saved = await api.saveTemplate({
        ...(view ? { id: view.id } : {}),
        name: draft.name,
        body: bodyFromDraft(draft),
        ...(note.trim() ? { note: note.trim() } : {}),
        base_revision: view?.latest_revision ?? 0,
      });
      toast.success(`Saved r${saved.revision}`);
      if (saved.warnings?.length) {
        toast.warning("Check these references", { description: saved.warnings.join("\n") });
      }
      setNote("");
      if (!view) {
        navigate(`/org/templates/${saved.id}`, { replace: true });
        return;
      }
      await load(false);
    } catch (e) {
      if (e instanceof ApiError && e.status === 409 && view) {
        // Someone saved meanwhile: name their revision; Reload takes it.
        const latest = await api.getTemplate(view.id).catch(() => null);
        setConflict(latest?.latest_revision ?? view.latest_revision + 1);
      } else {
        toast.error("Couldn't save the template", { description: e instanceof Error ? e.message : undefined });
      }
    } finally {
      setSaving(false);
    }
  };

  const remove = async () => {
    if (!view) return;
    try {
      await api.deleteTemplate(view.id);
      toast.success(`Template ${view.name} deleted`);
      navigate("/org/templates");
    } catch (e) {
      toast.error("Couldn't delete the template", { description: e instanceof Error ? e.message : undefined });
    }
  };

  if (loadError) {
    if (loadError instanceof ApiError && loadError.status === 404) {
      return <NotFound title="Template not found" description="This template doesn't exist in your organization." />;
    }
    return <LoadError message={loadError.message} onRetry={() => void load()} />;
  }
  if (templateId && !view) return <PageSpinner />;

  const live = instances.length;
  const editor = (
    <>
      <StickyActionBar>
        {conflict !== null ? (
          <p role="alert" className="mr-auto flex items-center gap-2 text-sm text-destructive">
            Someone saved r{conflict} meanwhile; reload to get it (your changes here are dropped).
            <Button size="sm" variant="outline" onClick={() => void load()}>
              Reload
            </Button>
          </p>
        ) : view ? (
          <Input
            aria-label="Revision note"
            value={note}
            placeholder="What changed (optional)"
            className="mr-auto max-w-md"
            onChange={(e) => setNote(e.target.value)}
          />
        ) : (
          // Revision 1 has nothing to note.
          <span className="mr-auto" />
        )}
        {problem ? (
          <InlineError icon className="max-w-md">
            {problem}
          </InlineError>
        ) : null}
        <Button onClick={() => void save()} disabled={saving || (!dirty && !!view) || conflict !== null}>
          {saving ? "Saving…" : view ? `Save as r${view.latest_revision + 1}` : "Save template"}
        </Button>
      </StickyActionBar>
      <TemplateEditor draft={draft} onChange={setDraft} />
    </>
  );

  return (
    <div className="mx-auto w-full max-w-5xl space-y-4">
      <header className="flex flex-wrap items-center gap-x-4 gap-y-2">
        <div className="mr-auto min-w-0">
          <h1 className="truncate text-2xl font-semibold tracking-tight">{view?.name ?? "Add template"}</h1>
          <p className="text-sm text-muted-foreground">
            {view
              ? `Revision ${view.latest_revision} · ${live} ${live === 1 ? "app uses" : "apps use"} it`
              : "Saving adds revision 1."}
          </p>
        </div>
        {view ? (
          <ConfirmButton
            trigger={
              <Button
                variant="outline"
                size="sm"
                className="text-destructive"
                disabled={live > 0}
                title={live > 0 ? "Delete or unlink its apps first" : undefined}
              >
                Delete template
              </Button>
            }
            title={`Delete ${view.name}?`}
            description="Its revisions are deleted. No app uses it; apps unlinked from it stay as they are."
            confirmLabel="Delete"
            onConfirm={() => void remove()}
          />
        ) : null}
      </header>

      <RolloutStrip rollout={rollout} serverName={serverName} onStop={stop} />

      {prefill ? (
        <div className="space-y-2 rounded-md border bg-muted/30 px-4 py-3 text-sm text-muted-foreground">
          <p>
            Built from <span className="font-medium text-foreground">{prefill.appName}</span>. Replace the parts that
            differ per app — hosts, ports — with <code className="font-mono">{"${PARAM}"}</code> parameters before saving.
          </p>
          {prefill.notes.length ? (
            <ul className="list-disc space-y-1 pl-4">
              {prefill.notes.map((n) => (
                <li key={n}>{n}</li>
              ))}
            </ul>
          ) : null}
        </div>
      ) : null}

      {view ? (
        <Tabs value={tab} onValueChange={(v) => setParams(v === "editor" ? {} : { tab: v }, { replace: true })}>
          <TabsList>
            <TabsTrigger value="editor">Editor</TabsTrigger>
            <TabsTrigger value="revisions">Revisions</TabsTrigger>
            <TabsTrigger value="instances">Instances</TabsTrigger>
          </TabsList>
          <TabsContent value="editor" className="mt-4">
            {editor}
          </TabsContent>
          <TabsContent value="revisions" className="mt-4">
            <TemplateRevisions view={view} />
          </TabsContent>
          <TabsContent value="instances" className="mt-4">
            <TemplateInstances
              view={view}
              instances={instances}
              onChanged={reloadInstances}
              onUpgradeAll={() => void upgradeAll()}
              rolloutActive={!!rollout && isActive(rollout)}
            />
          </TabsContent>
        </Tabs>
      ) : (
        editor
      )}
    </div>
  );
}
