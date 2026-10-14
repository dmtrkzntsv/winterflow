import { useMemo, useState } from "react";
import { ChevronDown, Plus, Trash2 } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { Disclosure } from "@/components/disclosure";
import { EditorSection } from "@/components/editor-section";
import { FilesSection } from "@/components/files-section";
import { IconPicker } from "@/components/icon-picker";
import { EmptyNote } from "@/components/page-states";
import {
  backupNamesIn,
  composeContents,
  ingressRow,
  paramRow,
  type IngressDraft,
  type ParamRow,
  type TemplateDraft,
} from "@/lib/template-draft";
import { compilePattern } from "@/lib/template-params";
import { cn } from "@/lib/utils";

// PARAM_COLS lays a parameter row (and the column heads) out: name,
// default and the row's two buttons on one line from sm up; on a phone the
// default goes under the name (the buttons keep 40px tap targets).
const PARAM_COLS =
  "grid-cols-[minmax(0,1fr)_5.5rem] sm:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_4.5rem]";

// paramFlags names the settings a closed parameter row hides.
function paramFlags(p: ParamRow): string[] {
  const flags: string[] = [];
  if (p.secret) flags.push("Secret");
  if (p.required) flags.push("Required");
  if (p.pattern !== "") flags.push("Pattern");
  return flags;
}

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
  // expanded holds the parameter rows showing their details; a parameter
  // just added opens with them shown.
  const [expanded, setExpanded] = useState<ReadonlySet<string>>(() => new Set());
  const toggleDetails = (id: string) =>
    setExpanded((prev) => {
      const next = new Set(prev);
      if (!next.delete(id)) next.add(id);
      return next;
    });
  const addParam = () => {
    const row = paramRow();
    set({ parameters: [...draft.parameters, row] });
    setExpanded((prev) => new Set(prev).add(row.id));
  };
  // Parsing the compose files runs only when their contents change.
  const [main, override] = composeContents(draft);
  const backupNames = useMemo(() => backupNamesIn([main, override]), [main, override]);

  const domains = (
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
  );

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
          <Button size="sm" variant="outline" onClick={addParam}>
            <Plus className="size-4" /> Add parameter
          </Button>
        }
      >
        {draft.parameters.length === 0 ? (
          <EmptyNote>No parameters yet.</EmptyNote>
        ) : (
          <div className="divide-y rounded-md border">
            <div
              role="group"
              aria-label="Parameter columns"
              className={cn(PARAM_COLS, "hidden gap-x-2 px-3 py-2 text-xs font-medium text-muted-foreground sm:grid")}
            >
              <span>Name</span>
              <span>Default</span>
            </div>
            {draft.parameters.map((p) => {
              const label = p.name || "parameter";
              const open = expanded.has(p.id);
              const detailsId = `param-details-${p.id}`;
              const unchecked = p.pattern !== "" && compilePattern(p.pattern) === null;
              const flags = paramFlags(p);
              return (
                <div key={p.id} className={cn(PARAM_COLS, "grid items-center gap-2 p-3")}>
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
                    placeholder={p.secret ? "No default (secret)" : "Default"}
                    // Phones: under the name, the buttons beside both — the
                    // order things are read and tabbed through is the DOM's.
                    className="font-mono max-sm:col-start-1"
                    onChange={(e) => setParam(p.id, { default: e.target.value })}
                  />
                  <div className="flex items-center justify-end gap-1 max-sm:col-start-2 max-sm:row-span-2 max-sm:row-start-1">
                    <Button
                      size="icon"
                      variant="ghost"
                      className="size-8 text-muted-foreground max-sm:size-10"
                      aria-label={`Details of ${label}`}
                      aria-expanded={open}
                      aria-controls={open ? detailsId : undefined}
                      onClick={() => toggleDetails(p.id)}
                    >
                      <ChevronDown
                        className={cn(
                          "size-4 transition-transform duration-150 motion-reduce:transition-none",
                          open && "rotate-180",
                        )}
                      />
                    </Button>
                    <Button
                      size="icon"
                      variant="ghost"
                      className="size-8 text-muted-foreground hover:text-destructive max-sm:size-10"
                      aria-label={`Remove ${label}`}
                      onClick={() => set({ parameters: removeById(draft.parameters, p.id) })}
                    >
                      <Trash2 className="size-4" />
                    </Button>
                  </div>
                  {!open && flags.length > 0 ? (
                    <p className="col-span-full text-xs text-muted-foreground">{flags.join(" · ")}</p>
                  ) : null}
                  {open ? (
                    <div id={detailsId} className="col-span-full grid gap-3 rounded-md bg-muted/30 p-3 sm:grid-cols-2">
                      <div className="grid gap-1.5">
                        <Label htmlFor={`${detailsId}-description`}>Description</Label>
                        <Input
                          id={`${detailsId}-description`}
                          aria-label={`Description of ${label}`}
                          value={p.description}
                          placeholder="Shown next to the field when adding an app"
                          onChange={(e) => setParam(p.id, { description: e.target.value })}
                        />
                      </div>
                      <div className="grid gap-1.5">
                        <Label htmlFor={`${detailsId}-pattern`}>Pattern</Label>
                        <Input
                          id={`${detailsId}-pattern`}
                          aria-label={`Pattern of ${label}`}
                          value={p.pattern}
                          placeholder="e.g. ^[a-z0-9-]+$"
                          className="font-mono"
                          onChange={(e) => setParam(p.id, { pattern: e.target.value })}
                        />
                      </div>
                      <div className="flex flex-wrap items-center gap-x-6 gap-y-2 text-sm sm:col-span-2">
                        <label className="flex min-h-10 items-center gap-2">
                          <Checkbox
                            aria-label={`${label} is secret`}
                            className="rounded-[4px]"
                            checked={p.secret}
                            onCheckedChange={(c) =>
                              setParam(p.id, { secret: c === true, default: c === true ? "" : p.default })
                            }
                          />
                          Secret (encrypted for the app's server, no default)
                        </label>
                        <label className="flex min-h-10 items-center gap-2">
                          <Checkbox
                            aria-label={`${label} is required`}
                            className="rounded-[4px]"
                            checked={p.required}
                            onCheckedChange={(c) => setParam(p.id, { required: c === true })}
                          />
                          Required
                        </label>
                      </div>
                      {unchecked ? (
                        <p className="text-xs text-muted-foreground sm:col-span-2">
                          The browser can't check this pattern; values are checked when the app is saved.
                        </p>
                      ) : null}
                    </div>
                  ) : null}
                </div>
              );
            })}
          </div>
        )}
      </EditorSection>

      {draft.ingress.length > 0 ? (
        domains
      ) : (
        // No hostnames yet: the section waits behind More options (most
        // templates publish a port and need none).
        <Disclosure className="border-t py-4">{domains}</Disclosure>
      )}

      {/* Only for a compose file that declares backups: there is nothing
          to set otherwise. */}
      {backupNames.length > 0 ? (
        <EditorSection
          title="Backup defaults"
          description={
            <>
              What each new app starts with, per backup the compose file declares under{" "}
              <code>x-winterflow.backups</code>. The storage is looked up by name on the app's server.
            </>
          }
        >
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
        </EditorSection>
      ) : null}
    </div>
  );
}
