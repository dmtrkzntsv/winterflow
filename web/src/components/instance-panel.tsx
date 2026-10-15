import { useEffect, useMemo, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { ArrowUpCircle, FileText } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { CodeEditor } from "@/components/code-editor";
import { ConfigBar } from "@/components/config-bar";
import { EditorSection } from "@/components/editor-section";
import { InlineError } from "@/components/inline-error";
import { VariableFields } from "@/components/variable-fields";
import { useTemplatesApi } from "@/hooks/use-templates-api";
import { focusFirstInvalid } from "@/lib/field-errors";
import { splitValues, variableErrors } from "@/lib/template-values";
import { sealSecrets, type InstanceView, type TemplateView } from "@/lib/templates";
import { templatePath } from "@/lib/routes";

// storedValues are the form's starting values: the plain values the
// instance holds (secrets start blank: blank keeps them).
function storedValues(current: TemplateView, instance: InstanceView): Record<string, string> {
  const out: Record<string, string> = {};
  for (const v of current.detected.variables ?? []) if (!v.encrypted) out[v.name] = instance.values?.[v.name] ?? "";
  return out;
}

// InstancePanel is the Configuration tab of an app a template manages: the
// template's files read-only, and in place of the editor the form over the
// variables its compose files declare, whose Save re-renders and redeploys
// the app. A value cleared goes back to the compose file's own. A secret
// keeps its value unless a new one is typed; a pending instance (never
// created) holds none, so every required secret is asked for again. When
// the template has a newer version, an Update opens the page's update
// dialog.
export function InstancePanel({
  instance,
  current,
  onSaved,
  onOpenHistory,
  latestRevision,
  onUpgrade,
  onDirtyChange,
}: {
  instance: InstanceView;
  current: TemplateView;
  onSaved: () => void;
  // onOpenHistory opens the page's History from the bar.
  onOpenHistory?: () => void;
  // latestRevision is the template's newest version; onUpgrade opens the
  // page's update dialog.
  latestRevision?: number;
  onUpgrade?: () => void;
  // onDirtyChange hears whether the form holds unsaved values (the page
  // asks before leaving them), and false when it goes.
  onDirtyChange?: (dirty: boolean) => void;
}) {
  const api = useTemplatesApi();
  const initial = useMemo(() => storedValues(current, instance), [current, instance]);
  const [values, setValues] = useState(initial);
  const [tried, setTried] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const root = useRef<HTMLDivElement>(null);
  const vars = current.detected.variables ?? [];
  const hosts = instance.hosts ?? [];
  const kept = instance.state === "created" ? Object.keys(instance.secrets ?? {}) : [];
  const dirty = vars.some((v) => (values[v.name] ?? "") !== (initial[v.name] ?? ""));
  useEffect(() => {
    onDirtyChange?.(dirty);
  }, [dirty, onDirtyChange]);
  useEffect(() => () => onDirtyChange?.(false), [onDirtyChange]);
  const refused = tried && Object.keys(variableErrors(vars, values, kept)).length > 0;
  const newer = onUpgrade !== undefined && latestRevision !== undefined && latestRevision > instance.revision;
  const note = (
    <>
      Managed by template{" "}
      <Link to={templatePath(instance.template_id)} className="font-medium text-foreground hover:underline">
        {instance.template_name}
      </Link>{" "}
      (version {instance.revision}). Change its files in the template, or detach the app from it in Settings.
    </>
  );

  const save = async () => {
    setTried(true);
    if (Object.keys(variableErrors(vars, values, kept)).length > 0) {
      // VariableFields marks each bad field once tried is set.
      focusFirstInvalid(root.current);
      return;
    }
    setSaving(true);
    setError("");
    try {
      const { plain, typed } = splitValues(vars, values);
      // Only what changed; a cleared value ("") goes back to the compose
      // file's own.
      const changed = Object.fromEntries(Object.entries(plain).filter(([name, v]) => v !== (initial[name] ?? "")));
      const secrets = await sealSecrets(api, instance.server_id, typed);
      await api.updateInstance({
        server_id: instance.server_id,
        key: instance.key,
        ...(Object.keys(changed).length ? { values: changed } : {}),
        ...(Object.keys(secrets).length ? { secrets } : {}),
      });
      toast.success("Saved and redeployed");
      onSaved();
    } catch (e) {
      setError(e instanceof Error ? e.message : "The save failed");
    } finally {
      setSaving(false);
    }
  };

  return (
    <div ref={root}>
      <ConfigBar
        status={
          refused ? (
            <span role="alert" tabIndex={-1} data-refusal className="text-destructive">
              Some fields need attention.
            </span>
          ) : dirty ? (
            <span className="inline-flex items-center gap-2 text-foreground">
              <span className="size-2 rounded-full bg-status-warn" aria-hidden />
              Unsaved changes
            </span>
          ) : (
            note
          )
        }
        // A pending instance's save is a retry of its create: always offered.
        dirty={dirty || instance.state !== "created" || refused}
        onOpenHistory={onOpenHistory}
        primary={
          <Button onClick={() => void save()} disabled={saving || (!dirty && instance.state === "created")}>
            {saving ? "Saving…" : "Save & redeploy"}
          </Button>
        }
      />
      {newer ? (
        <div role="status" className="mb-6 flex flex-wrap items-center gap-3 rounded-lg border bg-muted/40 px-4 py-3 text-sm">
          <ArrowUpCircle className="size-4 shrink-0 text-primary" aria-hidden />
          <span className="mr-auto">
            A newer version of {instance.template_name} is available (version {latestRevision}).
          </span>
          <Button size="sm" variant="outline" onClick={onUpgrade}>
            Update
          </Button>
        </div>
      ) : null}
      {error ? (
        <InlineError icon className="mb-4">
          {error}
        </InlineError>
      ) : null}
      <EditorSection
        title="Variables"
        description="The values this app was created with; a field left empty keeps the compose file's own. Secret values are encrypted in your browser for this server."
      >
        {vars.length === 0 ? (
          <p className="text-sm text-muted-foreground">The template takes no values.</p>
        ) : (
          <VariableFields
            vars={vars}
            values={values}
            onChange={(name, v) => setValues((prev) => ({ ...prev, [name]: v }))}
            kept={kept}
            showRequired={tried}
          />
        )}
      </EditorSection>
      {hosts.length > 0 ? (
        <EditorSection title="Hostnames" description="Set when the app was created from the template.">
          <ul className="space-y-1 text-sm">
            {hosts.map((h) => (
              <li key={h.host}>
                <span className="font-mono">{h.host}</span>
                <span className="text-muted-foreground"> → {h.expose}</span>
              </li>
            ))}
          </ul>
        </EditorSection>
      ) : null}
      <EditorSection title="Files" description="Read-only: the template's files, as the app runs them.">
        {Object.entries(current.body.files ?? {})
          .sort(([a], [b]) => a.localeCompare(b))
          .map(([name, content]) => (
            <div key={name} className="overflow-hidden rounded-md border">
              <div className="flex items-center gap-2 border-b bg-muted/40 px-3 py-2 font-mono text-sm">
                <FileText className="size-4 text-muted-foreground" />
                {name}
              </div>
              <CodeEditor value={content} filename={name} readOnly className="rounded-none border-0" />
            </div>
          ))}
      </EditorSection>
    </div>
  );
}
