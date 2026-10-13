import { Plus, Trash2 } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { EditorSection } from "@/components/editor-section";
import { FilesSection } from "@/components/files-section";
import { IconPicker } from "@/components/icon-picker";
import { EmptyNote } from "@/components/page-states";
import {
  ingressRow,
  paramRow,
  recipeBackupNames,
  type IngressDraft,
  type ParamRow,
  type TemplateDraft,
} from "@/lib/template-draft";
import { compilePattern } from "@/lib/template-params";
import { cn } from "@/lib/utils";

// PARAM_GRID lays a parameter row (and the column heads) out on one line
// where the section is wide enough.
const PARAM_GRID =
  "@2xl:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_minmax(0,1fr)_minmax(0,1.4fr)_3rem_3.75rem_2rem] @2xl:gap-x-2";

// patchById and removeById edit one row of a list by its id.
function patchById<T extends { id: string }>(rows: T[], id: string, patch: Partial<T>): T[] {
  return rows.map((r) => (r.id === id ? { ...r, ...patch } : r));
}
function removeById<T extends { id: string }>(rows: T[], id: string): T[] {
  return rows.filter((r) => r.id !== id);
}

type Props = {
  draft: TemplateDraft;
  onChange: (next: TemplateDraft) => void;
};

// TemplateEditor edits a template revision: the app's files (the app
// editor's file editing, without secrets), the parameters an instance
// supplies, the app's name, icon and color, the ingress rows and the
// backup defaults.
export function TemplateEditor({ draft, onChange }: Props) {
  const set = (patch: Partial<TemplateDraft>) => onChange({ ...draft, ...patch });
  const setParam = (id: string, patch: Partial<ParamRow>) => set({ parameters: patchById(draft.parameters, id, patch) });
  const setIngress = (id: string, patch: Partial<IngressDraft>) => set({ ingress: patchById(draft.ingress, id, patch) });
  const backupNames = recipeBackupNames(draft);

  return (
    <div>
      <EditorSection title="Template" description="Lowercase letters, digits and hyphens; unique in the organization.">
        <div className="grid gap-2">
          <Label htmlFor="template-name">Template name</Label>
          <Input
            id="template-name"
            value={draft.name}
            placeholder="my-stack"
            className="font-mono"
            onChange={(e) => set({ name: e.target.value })}
          />
        </div>
      </EditorSection>

      <EditorSection
        title="App"
        description={
          <>
            How each app added from the template appears. The name may use <code>{"${PARAM}"}</code> of a plain
            parameter.
          </>
        }
      >
        <div className="grid grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-x-3 gap-y-2">
          <Label>Icon</Label>
          <Label htmlFor="template-app-name">App name</Label>
          <Label htmlFor="template-color">Color</Label>
          <IconPicker value={draft.icon} color={draft.color} name={draft.appName} onChange={(icon) => set({ icon })} />
          <Input
            id="template-app-name"
            value={draft.appName}
            placeholder="my-app or ${NAME}"
            onChange={(e) => set({ appName: e.target.value })}
          />
          <Input
            id="template-color"
            type="color"
            className="h-9 w-14 cursor-pointer p-1"
            value={draft.color || "#64748b"}
            onChange={(e) => set({ color: e.target.value })}
          />
        </div>
      </EditorSection>

      <FilesSection
        files={draft.files}
        contents={draft.contents}
        onChange={(files, contents) => set({ files, contents })}
        allowSecret={false}
        imageChips={false}
        description={
          <>
            Copied unchanged into each app. <code>{"${PARAM}"}</code> takes a parameter's value at deploy; any other{" "}
            <code>{"${VAR}"}</code> must come from a shared variable on the server.
          </>
        }
      />

      <EditorSection
        title="Parameters"
        description="Each becomes an app variable of the same name. A secret's value is encrypted in the browser for the app's server; a template never holds one."
        action={
          <Button size="sm" variant="outline" onClick={() => set({ parameters: [...draft.parameters, paramRow()] })}>
            <Plus className="size-4" /> Add parameter
          </Button>
        }
      >
        {draft.parameters.length === 0 ? (
          <EmptyNote>No parameters yet.</EmptyNote>
        ) : (
          // One line per parameter under column heads where the section is
          // wide enough; narrower, each row wraps and names its checkboxes.
          <div className="@container">
            <div className="divide-y rounded-md border">
              <div
                role="group"
                aria-label="Parameter columns"
                className={cn(PARAM_GRID, "hidden px-3 py-2 text-xs font-medium text-muted-foreground @2xl:grid")}
              >
                <span>Name</span>
                <span>Default</span>
                <span>Pattern</span>
                <span>Description</span>
                <span className="text-center">Secret</span>
                <span className="text-center">Required</span>
              </div>
              {draft.parameters.map((p) => {
                const label = p.name || "parameter";
                const unchecked = p.pattern !== "" && compilePattern(p.pattern) === null;
                return (
                  <div key={p.id} className={cn(PARAM_GRID, "grid grid-cols-2 items-center gap-2 p-3")}>
                    <Input
                      aria-label="Parameter name"
                      value={p.name}
                      placeholder="NAME"
                      className="font-mono"
                      onChange={(e) => setParam(p.id, { name: e.target.value })}
                    />
                    <Input
                      aria-label={`Default of ${label}`}
                      value={p.secret ? "" : p.default}
                      disabled={p.secret}
                      placeholder={p.secret ? "No default" : "Default"}
                      className="font-mono"
                      onChange={(e) => setParam(p.id, { default: e.target.value })}
                    />
                    <Input
                      aria-label={`Pattern of ${label}`}
                      value={p.pattern}
                      placeholder="Pattern"
                      className="font-mono"
                      onChange={(e) => setParam(p.id, { pattern: e.target.value })}
                    />
                    <Input
                      aria-label={`Description of ${label}`}
                      value={p.description}
                      placeholder="Description"
                      onChange={(e) => setParam(p.id, { description: e.target.value })}
                    />
                    <div className="col-span-2 flex items-center gap-4 text-sm @2xl:contents">
                      <label className="flex items-center gap-2 @2xl:justify-center">
                        <Checkbox
                          aria-label={`${label} is secret`}
                          className="rounded-[4px]"
                          checked={p.secret}
                          onCheckedChange={(c) =>
                            setParam(p.id, { secret: c === true, default: c === true ? "" : p.default })
                          }
                        />
                        <span className="@2xl:sr-only">Secret</span>
                      </label>
                      <label className="flex items-center gap-2 @2xl:justify-center">
                        <Checkbox
                          aria-label={`${label} is required`}
                          className="rounded-[4px]"
                          checked={p.required}
                          onCheckedChange={(c) => setParam(p.id, { required: c === true })}
                        />
                        <span className="@2xl:sr-only">Required</span>
                      </label>
                      <Button
                        size="icon"
                        variant="ghost"
                        className="ml-auto size-8 text-muted-foreground hover:text-destructive @2xl:ml-0"
                        aria-label={`Remove ${label}`}
                        onClick={() => set({ parameters: removeById(draft.parameters, p.id) })}
                      >
                        <Trash2 className="size-4" />
                      </Button>
                    </div>
                    {unchecked ? (
                      <p className="col-span-full text-xs text-muted-foreground">
                        The browser can't check this pattern; values are checked when the app is saved.
                      </p>
                    ) : null}
                  </div>
                );
              })}
            </div>
          </div>
        )}
      </EditorSection>

      <EditorSection
        title="Domains & Routing"
        description={
          <>
            Hostnames routed to a port the compose file publishes on the host. Host and port may use{" "}
            <code>{"${PARAM}"}</code> of a plain parameter; the service is a note for people.
          </>
        }
        action={
          <Button size="sm" variant="outline" onClick={() => set({ ingress: [...draft.ingress, ingressRow()] })}>
            <Plus className="size-4" /> Add hostname
          </Button>
        }
      >
        {draft.ingress.length === 0 ? (
          <EmptyNote>No hostnames.</EmptyNote>
        ) : (
          <div className="divide-y rounded-md border">
            {draft.ingress.map((r) => (
              <div key={r.id} className="flex flex-wrap items-center gap-2 p-2">
                <Input
                  aria-label="Host"
                  value={r.host}
                  placeholder="${NAME}.example.com"
                  className="min-w-48 flex-1 font-mono"
                  onChange={(e) => setIngress(r.id, { host: e.target.value })}
                />
                <Input
                  aria-label={`Port of ${r.host || "host"}`}
                  value={r.port}
                  placeholder="8080 or ${PORT}"
                  className="w-36 font-mono"
                  onChange={(e) => setIngress(r.id, { port: e.target.value })}
                />
                <Input
                  aria-label={`Service of ${r.host || "host"}`}
                  value={r.service}
                  placeholder="Service (optional)"
                  className="w-40"
                  onChange={(e) => setIngress(r.id, { service: e.target.value })}
                />
                <Button
                  size="icon"
                  variant="ghost"
                  className="size-8 text-muted-foreground hover:text-destructive"
                  aria-label={`Remove ${r.host || "host"}`}
                  onClick={() => set({ ingress: removeById(draft.ingress, r.id) })}
                >
                  <Trash2 className="size-4" />
                </Button>
              </div>
            ))}
          </div>
        )}
      </EditorSection>

      <EditorSection
        title="Backup defaults"
        description={
          <>
            What each new app starts with, per backup the compose file declares under{" "}
            <code>x-winterflow.backups</code>. The storage is looked up by name on the app's server.
          </>
        }
      >
        {backupNames.length === 0 ? (
          <EmptyNote>The compose file declares no backups.</EmptyNote>
        ) : (
          <div className="divide-y rounded-md border">
            {backupNames.map((name) => {
              const def = draft.backups[name] ?? { enabled: false };
              const setDef = (patch: Partial<typeof def>) =>
                set({ backups: { ...draft.backups, [name]: { ...def, ...patch } } });
              return (
                <div key={name} className="flex flex-wrap items-center gap-3 p-2">
                  <Switch aria-label={`Back up ${name}`} checked={def.enabled} onCheckedChange={(on) => setDef({ enabled: on })} />
                  <code className="w-32 font-mono text-sm">{name}</code>
                  <Input
                    aria-label={`Storage of ${name}`}
                    value={def.storage ?? ""}
                    placeholder="Storage name"
                    className="w-48"
                    onChange={(e) => setDef({ storage: e.target.value })}
                  />
                </div>
              );
            })}
          </div>
        )}
      </EditorSection>
    </div>
  );
}
