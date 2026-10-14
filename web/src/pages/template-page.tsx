import { useCallback, useEffect, useRef, useState } from "react";
import { useLocation, useNavigate, useParams } from "react-router-dom";
import { Ellipsis, Trash2 } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import { TabsContent } from "@/components/ui/tabs";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { StickyActionBar } from "@/components/editor-section";
import { InlineError } from "@/components/inline-error";
import { NotFound } from "@/components/not-found";
import { PageHeader } from "@/components/page-header";
import { LoadError, PageSpinner } from "@/components/page-states";
import { RolloutStrip } from "@/components/rollout-strip";
import { TemplateEditor } from "@/components/template-editor";
import { TemplateInstances } from "@/components/template-instances";
import { TemplateRevisions } from "@/components/template-revisions";
import { UnderlineTabs } from "@/components/underline-tabs";
import { useServers } from "@/context/use-servers";
import { useRollout } from "@/hooks/use-rollout";
import { useRolloutsApi } from "@/hooks/use-rollouts-api";
import { useTabParam } from "@/hooks/use-tab-param";
import { useTemplatesApi } from "@/hooks/use-templates-api";
import { isActive } from "@/lib/rollouts";
import { bodyFromDraft, draftFromBody, draftProblem, emptyDraft, type DraftState, type TemplateDraft } from "@/lib/template-draft";
import { ApiError, usageLabel, type InstanceView, type TemplateView } from "@/lib/templates";
import { TEMPLATES_PATH, templatePath } from "@/lib/routes";

const TABS = ["editor", "apps", "versions"] as const;
type Tab = (typeof TABS)[number];
// TAB_ALIASES opens links from before the rebuild (?tab=instances,
// ?tab=revisions) on the tabs' new names.
const TAB_ALIASES: Readonly<Record<string, Tab>> = { instances: "apps", revisions: "versions" };

const BACK = { to: TEMPLATES_PATH, label: "Templates" };

// sameDraft says whether d still holds what base held. A field the editor
// didn't touch keeps its reference; only the others are compared by value
// (typing a change and taking it back is clean again).
function sameDraft(d: TemplateDraft, base: TemplateDraft): boolean {
  return (Object.keys(d) as (keyof TemplateDraft)[]).every(
    (k) => d[k] === base[k] || JSON.stringify(d[k]) === JSON.stringify(base[k]),
  );
}

// TemplatePage is one template (/templates/:templateId) or a new one
// (/templates/new, optionally prefilled by Save as template): its editor,
// where Save adds a version, the apps using it (with Update all apps and
// the update's progress) and its versions with their changes. The tab
// lives in ?tab=.
export default function TemplatePage() {
  const { templateId } = useParams();
  const location = useLocation();
  const navigate = useNavigate();
  const api = useTemplatesApi();
  const rolloutsApi = useRolloutsApi();
  const { servers } = useServers();
  const prefill = (location.state as DraftState | null) ?? null;
  const [tab, setTab] = useTabParam<Tab>(TABS, "editor", { aliases: TAB_ALIASES });

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
  // conflict is the version someone saved while this editor was open.
  const [conflict, setConflict] = useState<number | null>(null);
  const [confirmDelete, setConfirmDelete] = useState(false);

  // load reads the template into the editor, and its apps unless only the
  // template changed (a save).
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

  // reloadInstances follows an update without touching the editor.
  const reloadInstances = useCallback(() => {
    if (templateId) api.listInstances({ templateId }).then(setInstances, () => {});
  }, [api, templateId]);

  // The template's "Update all apps" rollout: each step it takes (and its
  // end) moves an app, so the list follows. The first progress seen (the
  // rollout's first read, or one just queued) moved nothing the page's own
  // load did not read: only a change after it refreshes the list.
  const { rollout, set: setRollout, stop } = useRollout(rolloutsApi, "template_upgrade", templateId ?? null);
  const progress = rollout ? `${rollout.id}:${rollout.done}:${rollout.skipped}:${rollout.state}` : "";
  const lastProgress = useRef("");
  useEffect(() => {
    const prev = lastProgress.current;
    lastProgress.current = progress;
    if (prev && progress && prev !== progress) reloadInstances();
  }, [progress, reloadInstances]);
  const serverName = (id: string) => servers.find((s) => s.id === id)?.name ?? id;

  const upgradeAll = async () => {
    if (!view) return;
    try {
      setRollout(await rolloutsApi.upgradeAll(view.id, view.latest_revision));
    } catch (e) {
      toast.error("Couldn't start the update", { description: e instanceof Error ? e.message : undefined });
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
      toast.success(`Saved version ${saved.revision}`);
      if (saved.warnings?.length) {
        toast.warning("Check these references", { description: saved.warnings.join("\n") });
      }
      setNote("");
      if (!view) {
        navigate(templatePath(saved.id), { replace: true });
        return;
      }
      await load(false);
    } catch (e) {
      if (e instanceof ApiError && e.status === 409 && view) {
        // Someone saved meanwhile: name their version; Reload takes it.
        const latest = await api.getTemplate(view.id).catch(() => null);
        setConflict(latest?.latest_revision ?? view.latest_revision + 1);
      } else {
        toast.error("Couldn't save the template", { description: e instanceof Error ? e.message : undefined });
      }
    } finally {
      setSaving(false);
    }
  };

  // remove deletes the template; a refusal is reported and rethrown so the
  // confirm dialog stays open.
  const remove = async () => {
    if (!view) return;
    try {
      await api.deleteTemplate(view.id);
    } catch (e) {
      toast.error("Couldn't delete the template", { description: e instanceof Error ? e.message : undefined });
      throw e;
    }
    toast.success(`Deleted ${view.name}`);
    navigate(TEMPLATES_PATH);
  };

  const title = view?.name ?? (templateId ? "Template" : "New template");

  if (loadError) {
    if (loadError instanceof ApiError && loadError.status === 404) {
      return <NotFound title="Template not found" description="This template doesn't exist in your organization." />;
    }
    return (
      <div className="space-y-6">
        <PageHeader title={title} back={BACK} />
        <LoadError message={loadError.message} onRetry={() => void load()} />
      </div>
    );
  }
  if (templateId && !view) {
    return (
      <div className="space-y-6">
        <PageHeader title={title} back={BACK} />
        <PageSpinner />
      </div>
    );
  }

  const live = instances.length;
  const editor = (
    <>
      <StickyActionBar>
        {conflict !== null ? (
          <p role="alert" className="mr-auto flex flex-wrap items-center gap-2 text-sm text-destructive">
            Someone saved version {conflict} meanwhile; reload to get it (your changes here are dropped).
            <Button size="sm" variant="outline" onClick={() => void load()}>
              Reload
            </Button>
          </p>
        ) : view ? (
          <Input
            aria-label="Version note"
            value={note}
            placeholder="What changed (optional)"
            className="mr-auto max-w-md"
            onChange={(e) => setNote(e.target.value)}
          />
        ) : (
          // Version 1 has nothing to note.
          <span className="mr-auto" />
        )}
        {problem ? (
          <InlineError icon className="max-w-md">
            {problem}
          </InlineError>
        ) : null}
        <Button onClick={() => void save()} disabled={saving || (!dirty && !!view) || conflict !== null}>
          {saving ? "Saving…" : view ? `Save as version ${view.latest_revision + 1}` : "Save template"}
        </Button>
      </StickyActionBar>
      <TemplateEditor draft={draft} onChange={setDraft} />
    </>
  );

  return (
    <div className="space-y-6">
      <PageHeader
        title={title}
        subtitle={
          view
            ? `Version ${view.latest_revision} · ${usageLabel({ instances: live, behind: 0 }).toLowerCase()}`
            : "Saving creates version 1."
        }
        back={BACK}
        actions={
          view ? (
            // Not modal: the confirm dialog it opens takes the focus.
            <DropdownMenu modal={false}>
              <DropdownMenuTrigger asChild>
                <Button variant="outline" size="icon" className="size-10 sm:size-9" aria-label="More actions">
                  <Ellipsis className="size-4" />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end" className="max-w-72">
                <DropdownMenuItem
                  disabled={live > 0}
                  className="text-destructive focus:text-destructive"
                  onSelect={() => setConfirmDelete(true)}
                >
                  <Trash2 className="size-4" /> Delete template
                </DropdownMenuItem>
                {/* Beside the dimmed item, not in it: full-contrast text. */}
                {live > 0 ? (
                  <p className="px-2 py-1.5 text-xs text-muted-foreground">
                    Delete its apps or detach them from it first
                  </p>
                ) : null}
              </DropdownMenuContent>
            </DropdownMenu>
          ) : null
        }
      />

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
        <UnderlineTabs<Tab>
          value={tab}
          onValueChange={setTab}
          label="Template sections"
          items={[
            { value: "editor", label: "Editor" },
            { value: "apps", label: "Apps using it" },
            { value: "versions", label: "Versions" },
          ]}
        >
          <TabsContent value="editor" className="mt-4">
            {editor}
          </TabsContent>
          <TabsContent value="apps" className="mt-4">
            <TemplateInstances
              view={view}
              instances={instances}
              onChanged={reloadInstances}
              onUpgradeAll={() => void upgradeAll()}
              rolloutActive={!!rollout && isActive(rollout)}
            />
          </TabsContent>
          <TabsContent value="versions" className="mt-4">
            <TemplateRevisions view={view} />
          </TabsContent>
        </UnderlineTabs>
      ) : (
        editor
      )}

      {view ? (
        <ConfirmDialog
          open={confirmDelete}
          onOpenChange={setConfirmDelete}
          title={`Delete ${view.name}?`}
          description="Its versions are deleted too. Apps detached from it stay as they are."
          confirmLabel="Delete template"
          pendingLabel="Deleting…"
          onConfirm={remove}
        />
      ) : null}
    </div>
  );
}
