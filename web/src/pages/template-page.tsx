import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Link, useLocation, useNavigate, useParams } from "react-router-dom";
import { Ellipsis, Plus, Trash2 } from "lucide-react";
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
import { CatalogLogo } from "@/components/catalog-logo";
import { StickyActionBar } from "@/components/editor-section";
import { InlineError } from "@/components/inline-error";
import { NotFound } from "@/components/not-found";
import { PageHeader } from "@/components/page-header";
import { LoadError, PageSpinner } from "@/components/page-states";
import { RolloutStrip } from "@/components/rollout-strip";
import { TemplateDetected } from "@/components/template-detected";
import { TemplateEditor } from "@/components/template-editor";
import { TemplateInstances } from "@/components/template-instances";
import { TemplateRevisions } from "@/components/template-revisions";
import { UnderlineTabs } from "@/components/underline-tabs";
import { useServers } from "@/context/use-servers";
import { useRollout } from "@/hooks/use-rollout";
import { useRolloutsApi } from "@/hooks/use-rollouts-api";
import { useTabParam } from "@/hooks/use-tab-param";
import { useTemplateInspection } from "@/hooks/use-template-inspection";
import { useTemplatesApi } from "@/hooks/use-templates-api";
import { isActive } from "@/lib/rollouts";
import { bodyFromDraft, draftFromBody, draftProblem, emptyDraft, type DraftState, type TemplateDraft } from "@/lib/template-draft";
import { ApiError, usageLabel, type InstanceView, type TemplateView } from "@/lib/templates";
import { newAppFromTemplatePath, TEMPLATES_PATH, templatePath } from "@/lib/routes";

const TABS = ["editor", "apps", "versions"] as const;
type Tab = (typeof TABS)[number];
// TAB_ALIASES opens links from before the rebuild (?tab=instances,
// ?tab=revisions) on the tabs' new names.
const TAB_ALIASES: Readonly<Record<string, Tab>> = { instances: "apps", revisions: "versions" };

const BACK = { to: TEMPLATES_PATH, label: "Templates" };

function CatalogTemplatePage({
  view,
  instances,
  servers,
  activeServerId,
  rollout,
  stop,
  onUpgradeAll,
  onInstancesChanged,
}: {
  view: TemplateView;
  instances: InstanceView[];
  servers: { id: string; name: string }[];
  activeServerId: string | null;
  rollout: ReturnType<typeof useRollout>["rollout"];
  stop: ReturnType<typeof useRollout>["stop"];
  onUpgradeAll: () => Promise<void>;
  onInstancesChanged: () => void;
}) {
  const [confirmUpgrade, setConfirmUpgrade] = useState(false);
  const behind = instances.filter((i) => i.state === "created" && i.revision < view.latest_revision).length;
  const files = Object.entries(view.body.files ?? {}).sort(([a], [b]) => a.localeCompare(b));
  const catalog = view.catalog;
  return (
    <div className="space-y-6">
      <PageHeader
        title={view.name}
        subtitle={catalog?.summary}
        back={BACK}
        actions={!view.withdrawn_at
          ? <CreateAppButton templateId={view.id} servers={servers} activeServerId={activeServerId} />
          : null}
      />
      <div className="flex items-start gap-4">
        <CatalogLogo template={view} className="size-16 shrink-0 text-xl" />
        <div className="min-w-0 space-y-2 text-sm">
          {catalog?.description ? <p>{catalog.description}</p> : null}
          <div className="flex flex-wrap gap-x-4 gap-y-1 text-muted-foreground">
            {catalog?.license ? <span>License: {catalog.license}</span> : null}
            {catalog?.homepage_url ? <a href={catalog.homepage_url} target="_blank" rel="noreferrer" className="text-foreground underline underline-offset-2">Homepage</a> : null}
          </div>
        </div>
      </div>
      {view.withdrawn_at ? (
        <div role="note" className="rounded-md border bg-muted/30 px-4 py-3 text-sm">
          Withdrawn from the catalog. Existing apps and versions remain available.
        </div>
      ) : null}
      <RolloutStrip rollout={rollout} serverName={(id) => servers.find((s) => s.id === id)?.name ?? "a server"} onStop={stop} />
      {behind > 0 ? (
        <div className="flex flex-wrap items-center justify-between gap-3 rounded-xl border bg-muted/30 px-4 py-3 text-sm">
          <span>{behind} {behind === 1 ? "app has" : "apps have"} an upgrade available.</span>
          <Button disabled={!!rollout && isActive(rollout)} onClick={() => setConfirmUpgrade(true)}>Upgrade all</Button>
        </div>
      ) : null}
      <section aria-labelledby="catalog-files-heading" className="space-y-3">
        <h2 id="catalog-files-heading" className="text-lg font-semibold">Files</h2>
        {files.length ? files.map(([filename, content]) => (
          <div key={filename} className="overflow-hidden rounded-md border">
            <h3 className="border-b bg-muted/40 px-3 py-2 font-mono text-xs">{filename}</h3>
            <pre aria-label={`Content of ${filename}`} className="max-h-96 overflow-auto p-3 font-mono text-xs leading-5">{content}</pre>
          </div>
        )) : <p className="text-sm text-muted-foreground">No files in this version.</p>}
      </section>
      <section aria-labelledby="catalog-versions-heading" className="space-y-3">
        <h2 id="catalog-versions-heading" className="text-lg font-semibold">Versions</h2>
        <TemplateRevisions view={view} />
      </section>
      <section aria-labelledby="catalog-apps-heading" className="space-y-3">
        <h2 id="catalog-apps-heading" className="text-lg font-semibold">Apps using it</h2>
        <TemplateInstances view={view} instances={instances} onChanged={onInstancesChanged} />
      </section>
      <ConfirmDialog
        open={confirmUpgrade}
        onOpenChange={setConfirmUpgrade}
        destructive={false}
        title={`Upgrade ${behind} ${behind === 1 ? "app" : "apps"} to version ${view.latest_revision}?`}
        description="Apps are updated one at a time. An app needing a new value is skipped; the first failure stops the rest."
        confirmLabel="Upgrade all"
        onConfirm={onUpgradeAll}
      />
    </div>
  );
}

// sameDraft says whether d still holds what base held. A field the editor
// didn't touch keeps its reference; only the others are compared by value
// (typing a change and taking it back is clean again).
function sameDraft(d: TemplateDraft, base: TemplateDraft): boolean {
  return (Object.keys(d) as (keyof TemplateDraft)[]).every(
    (k) => d[k] === base[k] || JSON.stringify(d[k]) === JSON.stringify(base[k]),
  );
}

// CreateAppButton is a template page's primary: New app → From a template
// with this template picked, on the active server; with several servers it
// asks which one.
function CreateAppButton({
  templateId,
  servers,
  activeServerId,
}: {
  templateId: string;
  servers: { id: string; name: string }[];
  activeServerId: string | null;
}) {
  const label = (
    <>
      <Plus className="size-4" aria-hidden /> Create app
    </>
  );
  if (servers.length > 1) {
    return (
      <DropdownMenu modal={false}>
        <DropdownMenuTrigger asChild>
          <Button>{label}</Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end">
          {servers.map((s) => (
            <DropdownMenuItem key={s.id} asChild>
              <Link to={newAppFromTemplatePath(s.id, templateId)}>On {s.name}</Link>
            </DropdownMenuItem>
          ))}
        </DropdownMenuContent>
      </DropdownMenu>
    );
  }
  const serverId = servers[0]?.id ?? activeServerId;
  if (!serverId) return null;
  return (
    <Button asChild>
      <Link to={newAppFromTemplatePath(serverId, templateId)}>{label}</Link>
    </Button>
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
  const { servers, activeServerId } = useServers();
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
  const serverName = (id: string) => servers.find((s) => s.id === id)?.name ?? "a server";

  const upgradeAll = async () => {
    if (!view) return;
    try {
      setRollout(await rolloutsApi.upgradeAll(view.id, view.latest_revision));
    } catch (e) {
      toast.error("Couldn't start the update", { description: e instanceof Error ? e.message : undefined });
    }
  };

  const dirty = baseline === null || !sameDraft(draft, baseline);
  // The Detected panel: a saved version's own reading until the draft
  // changes, else the API's reading of the draft (inspect-template). An
  // existing template's placeholder draft (while it loads, or when it
  // can't be read) is never read.
  const body = useMemo(() => bodyFromDraft(draft), [draft]);
  const reading = !loadError && (dirty || (!view && !templateId));
  const inspected = useTemplateInspection(api, body, reading);
  const detectedPanel = reading ? (
    <TemplateDetected
      detected={inspected.inspection?.detected ?? null}
      problem={inspected.checking ? "" : (inspected.inspection?.problem ?? "")}
      error={inspected.error}
      checking={inspected.checking}
    />
  ) : (
    <TemplateDetected detected={view?.detected ?? null} />
  );

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
        body,
        ...(note.trim() ? { note: note.trim() } : {}),
        base_revision: view?.latest_revision ?? 0,
      });
      toast.success(`Saved version ${saved.revision}`);
      if (saved.warnings?.length) {
        toast.warning("Check these warnings", { description: saved.warnings.join("\n") });
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

  if (view?.source === "catalog") {
    return (
      <CatalogTemplatePage
        view={view}
        instances={instances}
        servers={servers}
        activeServerId={activeServerId}
        rollout={rollout}
        stop={stop}
        onUpgradeAll={upgradeAll}
        onInstancesChanged={reloadInstances}
      />
    );
  }

  const live = instances.length;
  // A saved template shows its save bar (the note and Save as version N)
  // only once it is edited, or while a save or its outcome is on screen.
  const barShown = !view || dirty || saving || conflict !== null || !!problem;
  const editor = (
    <>
      {barShown ? (
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
      ) : null}
      <TemplateEditor draft={draft} onChange={setDraft} detected={detectedPanel} />
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
            <>
              <CreateAppButton templateId={view.id} servers={servers} activeServerId={activeServerId} />
              {/* Not modal: the confirm dialog it opens takes the focus. */}
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
            </>
          ) : null
        }
      />

      <RolloutStrip rollout={rollout} serverName={serverName} onStop={stop} />

      {prefill ? (
        <div className="space-y-2 rounded-md border bg-muted/30 px-4 py-3 text-sm text-muted-foreground">
          <p>
            Built from <span className="font-medium text-foreground">{prefill.appName}</span>. Before saving, declare under{" "}
            <code className="font-mono">x-winterflow</code> what differs per app: the variables each app fills in and the
            ports it exposes.
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
