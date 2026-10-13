import { useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { FileText } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { CodeEditor } from "@/components/code-editor";
import { EditorSection, StickyActionBar } from "@/components/editor-section";
import { InlineError } from "@/components/inline-error";
import { ParameterFields } from "@/components/parameter-fields";
import { useTemplatesApi } from "@/hooks/use-templates-api";
import { fieldErrors, splitValues } from "@/lib/template-params";
import { sealSecrets, type InstanceView, type TemplateView } from "@/lib/templates";

// storedValues are the form's starting values: the plain values the
// instance holds (secrets start blank: blank keeps them).
function storedValues(current: TemplateView, instance: InstanceView): Record<string, string> {
  const out: Record<string, string> = {};
  for (const p of current.body.parameters ?? []) if (!p.secret) out[p.name] = instance.parameters?.[p.name] ?? "";
  return out;
}

// InstancePanel is the Configuration tab of an app a template manages: the
// template's files read-only, and in place of the editor a Parameters form
// whose Save re-renders and redeploys the app. A secret keeps its value
// unless a new one is typed; a pending instance (never created) holds none,
// so every required secret is asked for again.
export function InstancePanel({
  instance,
  current,
  onSaved,
}: {
  instance: InstanceView;
  current: TemplateView;
  onSaved: () => void;
}) {
  const api = useTemplatesApi();
  const initial = useMemo(() => storedValues(current, instance), [current, instance]);
  const [values, setValues] = useState(initial);
  const [tried, setTried] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const params = current.body.parameters ?? [];
  const kept = instance.state === "created" ? Object.keys(instance.secrets ?? {}) : [];
  const dirty = params.some((p) => (values[p.name] ?? "") !== (initial[p.name] ?? ""));

  const save = async () => {
    setTried(true);
    if (Object.keys(fieldErrors(params, values, kept)).length > 0) return;
    setSaving(true);
    setError("");
    try {
      const { plain, typed } = splitValues(params, values);
      const parameters: Record<string, string> = {};
      for (const p of params) {
        const v = plain[p.name];
        if (v === undefined || v === (initial[p.name] ?? "")) continue;
        // A cleared value means "the default" here (the form says so), but
        // the API keeps a given "" as set: send the default itself.
        parameters[p.name] = v === "" && p.default ? p.default : v;
      }
      const secrets = await sealSecrets(api, instance.server_id, typed);
      await api.updateInstance({
        server_id: instance.server_id,
        key: instance.key,
        ...(Object.keys(parameters).length ? { parameters } : {}),
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
    <div>
      <StickyActionBar className="mb-6">
        <p className="mr-auto text-sm text-muted-foreground">
          Managed by template{" "}
          <Link to={`/org/templates/${instance.template_id}`} className="font-medium text-foreground hover:underline">
            {instance.template_name}
          </Link>{" "}
          r{instance.revision}. Change the files in the template, or unlink the app in Settings.
        </p>
        <Button onClick={() => void save()} disabled={saving || (!dirty && instance.state === "created")}>
          {saving ? "Saving…" : "Save & redeploy"}
        </Button>
      </StickyActionBar>
      {error ? (
        <InlineError icon className="mb-4">
          {error}
        </InlineError>
      ) : null}
      <EditorSection
        title="Parameters"
        description="The values this app was added with. Secret values are encrypted in your browser for this server."
      >
        {params.length === 0 ? (
          <p className="text-sm text-muted-foreground">The template takes no values.</p>
        ) : (
          <ParameterFields
            params={params}
            values={values}
            onChange={(name, v) => setValues((prev) => ({ ...prev, [name]: v }))}
            kept={kept}
            showRequired={tried}
          />
        )}
      </EditorSection>
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
