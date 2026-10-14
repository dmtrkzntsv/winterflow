import { useCallback, useEffect, useState } from "react";
import { Trash2 } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import type { Ownership } from "@/components/app-page/ownership";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { IconPicker } from "@/components/icon-picker";
import { InlineError } from "@/components/inline-error";
import { Section } from "@/components/section";
import type { App } from "@/context/apps-context-base";
import { useApps } from "@/context/use-apps";
import { useTemplatesApi } from "@/hooks/use-templates-api";
import { saveAppDetails } from "@/lib/app-details-save";
import { stateFromDetail } from "@/lib/app-editor-io";
import { isManagedByTemplate } from "@/lib/templates";

// The colour an app without one shows (the editor's default too).
const DEFAULT_COLOR = "#64748b";

// GeneralSection edits how the app appears: name, icon, colour and
// description, with one Save (see saveAppDetails for what each part does).
// Name, icon and colour come from the app list at once; the description is
// read from the app. A template-managed app changes its name only: the rest
// is its template's (the API refuses its save).
function GeneralSection({ app, ownership, running }: { app: App; ownership: Ownership; running: boolean }) {
  const deps = useApps();
  const { getApp, refresh } = deps;
  const managed = ownership !== null;
  const templateName = ownership?.instance?.template_name;
  const shown = { name: app.name, icon: app.icon ?? "", color: app.color || DEFAULT_COLOR };
  const [form, setForm] = useState({ ...shown, description: "" });
  // The stored description: null until read.
  const [description, setDescription] = useState<string | null>(null);
  const [readError, setReadError] = useState("");
  const [saving, setSaving] = useState(false);

  // When the app's saved name, icon or colour change (a rename made
  // elsewhere, the list refreshed after a save), the form starts from them
  // again: a stale form must not revert them on the next Save.
  const savedKey = JSON.stringify(shown);
  const [seededFrom, setSeededFrom] = useState(savedKey);
  if (seededFrom !== savedKey) {
    setSeededFrom(savedKey);
    setForm((f) => ({ ...shown, description: f.description }));
  }

  const readDescription = useCallback(async () => {
    setReadError("");
    try {
      const stored = stateFromDetail(app.id, await getApp(app.id)).config.description ?? "";
      setDescription(stored);
      setForm((f) => ({ ...f, description: stored }));
    } catch (e) {
      setReadError(e instanceof Error ? e.message : "the agent didn't answer");
    }
  }, [app.id, getApp]);

  useEffect(() => {
    void readDescription();
  }, [readDescription]);

  const before = { ...shown, description: description ?? "" };
  const after = { ...form, description: description === null ? "" : form.description };
  const dirty =
    after.name.trim() !== before.name.trim() ||
    after.icon !== before.icon ||
    after.color !== before.color ||
    after.description !== before.description;
  const canSave = dirty && form.name.trim() !== "" && !saving;

  const save = async () => {
    setSaving(true);
    try {
      const result = await saveAppDetails(deps, app.id, before, after, running);
      if (result.saved === "none") toast.success("App renamed");
      else if (result.saved === "deployed") toast.success("Saved");
      else toast.success("Saved", { description: "It goes live with the app's next deploy." });
      if (result.saved !== "none") void readDescription();
    } catch (e) {
      const message = e instanceof Error ? e.message : "";
      if (isManagedByTemplate(message)) {
        toast.error("This app is managed by a template", {
          description: "Its icon, colour and description come from the template.",
        });
        void refresh();
      } else {
        toast.error("Couldn't save", { description: message || undefined });
      }
    } finally {
      setSaving(false);
    }
  };

  return (
    <Section title="General" description="How the app appears in the sidebar and the app list.">
      <form
        className="grid gap-4 rounded-xl border bg-card p-4"
        onSubmit={(e) => {
          e.preventDefault();
          if (canSave) void save();
        }}
      >
        <div className="grid grid-cols-[auto_minmax(0,1fr)_auto] items-end gap-3">
          <div className="grid gap-1.5">
            <Label>Icon</Label>
            <IconPicker
              label="Icon"
              value={form.icon}
              color={form.color}
              name={form.name}
              disabled={managed || saving}
              onChange={(icon) => setForm((f) => ({ ...f, icon }))}
            />
          </div>
          <div className="grid gap-1.5">
            <Label htmlFor="app-name">Name</Label>
            <Input
              id="app-name"
              aria-label="App name"
              className="max-sm:h-10"
              value={form.name}
              disabled={saving}
              onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
            />
          </div>
          <div className="grid gap-1.5">
            <Label htmlFor="app-colour">Colour</Label>
            <Input
              id="app-colour"
              type="color"
              className="h-10 w-14 cursor-pointer p-1 sm:h-9"
              value={form.color}
              disabled={managed || saving}
              onChange={(e) => setForm((f) => ({ ...f, color: e.target.value }))}
            />
          </div>
        </div>
        <div className="grid gap-1.5">
          <Label htmlFor="app-description">Description</Label>
          <Input
            id="app-description"
            className="max-sm:h-10"
            value={form.description}
            placeholder={description === null && !readError ? "Loading…" : "Optional"}
            disabled={managed || saving || description === null}
            onChange={(e) => setForm((f) => ({ ...f, description: e.target.value }))}
          />
        </div>
        {readError ? (
          <InlineError>
            Couldn't read the description: {readError}.{" "}
            <button type="button" className="min-h-10 underline underline-offset-4 sm:min-h-0" onClick={() => void readDescription()}>
              Retry
            </button>
          </InlineError>
        ) : null}
        {managed ? (
          <p className="text-xs text-muted-foreground">
            Icon, colour and description are set by {templateName ? `template ${templateName}` : "its template"}.
          </p>
        ) : null}
        <div className="flex justify-end">
          <Button type="submit" className="max-sm:h-10" disabled={!canSave}>
            {saving ? "Saving…" : "Save"}
          </Button>
        </div>
      </form>
    </Section>
  );
}

// TemplateSection starts a template from a plain app, or detaches a
// template-managed one (once its first deploy from the template went
// through: the API detaches only a created instance). Nothing while the
// instance is still read.
function TemplateSection({
  ownership,
  onDetached,
  onSaveAsTemplate,
  savingTemplate,
}: {
  ownership: Ownership;
  onDetached: () => void;
  onSaveAsTemplate: () => void;
  savingTemplate: boolean;
}) {
  const templatesApi = useTemplatesApi();
  const [confirm, setConfirm] = useState(false);
  const instance = ownership?.instance ?? null;
  if (ownership && !instance) return null;

  if (!instance) {
    return (
      <Section
        title="Template"
        description="Start a template from this app: its files, its variables as parameters, its addresses and backup settings. The app stays as it is."
      >
        <div>
          <Button variant="outline" className="max-sm:h-10" disabled={savingTemplate} onClick={onSaveAsTemplate}>
            Save as template
          </Button>
        </div>
      </Section>
    );
  }

  const detach = async () => {
    try {
      await templatesApi.unlinkInstance(instance.server_id, instance.key);
      toast.success("Detached from the template");
      onDetached();
    } catch (e) {
      toast.error("Detach failed", { description: e instanceof Error ? e.message : undefined });
      throw e;
    }
  };

  return (
    <Section
      title="Template"
      description={`Managed by template ${instance.template_name}: its files and values change through the template.`}
    >
      {instance.state !== "created" ? (
        <p className="text-sm text-muted-foreground">Detach becomes available once its first deploy from the template succeeds.</p>
      ) : (
        <div>
          <Button variant="outline" className="max-sm:h-10" onClick={() => setConfirm(true)}>
            Detach from template
          </Button>
        </div>
      )}
      <ConfirmDialog
        open={confirm}
        onOpenChange={setConfirm}
        title={`Detach from ${instance.template_name}?`}
        description="The app becomes an ordinary app you can edit freely; template updates no longer reach it."
        confirmLabel="Detach"
        pendingLabel="Detaching…"
        destructive={false}
        onConfirm={detach}
      />
    </Section>
  );
}

// AppSettingsTab is the app page's Settings tab: General (name, icon,
// colour, description), Template (Save as / Detach) and the Danger zone
// (Delete, through the page's one Delete dialog).
export function AppSettingsTab({
  app,
  ownership,
  running,
  onDetached,
  onSaveAsTemplate,
  savingTemplate,
  onDelete,
}: {
  app: App;
  ownership: Ownership;
  running: boolean;
  onDetached: () => void;
  onSaveAsTemplate: () => void;
  savingTemplate: boolean;
  onDelete: () => void;
}) {
  return (
    <div className="max-w-3xl space-y-10">
      <GeneralSection app={app} ownership={ownership} running={running} />
      <TemplateSection
        ownership={ownership}
        onDetached={onDetached}
        onSaveAsTemplate={onSaveAsTemplate}
        savingTemplate={savingTemplate}
      />
      <Section
        title="Danger zone"
        description="Stops the containers and removes the deployment and its history. Volumes and backups are kept unless you choose otherwise."
      >
        <div>
          <Button
            variant="outline"
            className="border-destructive/40 text-destructive hover:bg-destructive/10 hover:text-destructive max-sm:h-10"
            onClick={onDelete}
          >
            <Trash2 className="size-4" aria-hidden /> Delete app…
          </Button>
        </div>
      </Section>
    </div>
  );
}
