import { useEffect, useState } from "react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { InlineError } from "@/components/inline-error";
import { ResponsiveDialog } from "@/components/responsive-dialog";
import { VariableFields } from "@/components/variable-fields";
import { useTemplatesApi } from "@/hooks/use-templates-api";
import { AppStatusCode } from "@/context/apps-context-base";
import { defaultChanges, splitValues, upgradeNeeds, variableErrors } from "@/lib/template-values";
import { sealSecrets, type InstanceView, type TemplateView } from "@/lib/templates";

type Props = {
  instance: InstanceView;
  // appName is the app's name as the page shows it.
  appName: string;
  // target is the version to move to (its body says what it needs).
  target: TemplateView;
  // current is the instance's own version when the caller has it read;
  // otherwise the dialog reads it (to name the defaults that change).
  current?: TemplateView | null;
  // onClose closes the dialog (the caller mounts it only while open).
  onClose: () => void;
  onDone: () => void;
};

// UpgradeInstanceDialog updates one app to another version of its template,
// asking only for the values the app doesn't hold yet (a newly required
// variable, a pending app's secrets), and naming the variables left to the
// compose file whose value the update changes.
export function UpgradeInstanceDialog({ instance, appName, target, current, onClose, onDone }: Props) {
  const api = useTemplatesApi();
  const needs = upgradeNeeds(target.detected.variables, instance);
  const given =
    current && current.id === instance.template_id && current.revision === instance.revision ? current : null;
  const [read, setRead] = useState<{ view: TemplateView | null; error: string } | null>(null);
  useEffect(() => {
    if (given) return;
    let live = true;
    api.getRevision(instance.template_id, instance.revision).then(
      (view) => live && setRead({ view, error: "" }),
      (e: unknown) => live && setRead({ view: null, error: e instanceof Error ? e.message : "the read failed" }),
    );
    return () => {
      live = false;
    };
  }, [api, given, instance.template_id, instance.revision]);
  const from = given ?? read?.view ?? null;
  const changes = from ? defaultChanges(from.detected.variables, target.detected.variables, instance) : [];
  const starts = instance.state === "created" && instance.status_code === AppStatusCode.Stopped;
  const [values, setValues] = useState<Record<string, string>>({});
  const [tried, setTried] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const upgrade = async () => {
    setTried(true);
    if (Object.keys(variableErrors(needs, values)).length > 0) return;
    setBusy(true);
    setError("");
    try {
      const { plain, typed } = splitValues(needs, values);
      const secrets = await sealSecrets(api, instance.server_id, typed);
      await api.upgradeInstance({
        server_id: instance.server_id,
        key: instance.key,
        revision: target.revision,
        ...(Object.keys(plain).length ? { values: plain } : {}),
        ...(Object.keys(secrets).length ? { secrets } : {}),
      });
      toast.success(`Updated to version ${target.revision}`);
      onClose();
      onDone();
    } catch (e) {
      setError(e instanceof Error ? e.message : "The update failed");
    } finally {
      setBusy(false);
    }
  };

  return (
    <ResponsiveDialog
      open
      onOpenChange={(open) => {
        if (!open && !busy) onClose();
      }}
      title={`Update ${appName} to version ${target.revision}`}
      description={
        <>
          The app is redeployed with {target.name} version {target.revision}; its backup settings and hostnames stay (a
          backup the version adds starts off). Values you set stay; variables left to the compose file take its new
          value.{starts ? " The app will be started." : ""}
        </>
      }
      footer={
        <>
          <Button variant="outline" disabled={busy} onClick={onClose}>
            Cancel
          </Button>
          <Button disabled={busy} onClick={() => void upgrade()}>
            {busy ? "Updating…" : `Update to version ${target.revision}`}
          </Button>
        </>
      }
    >
      <div className="grid gap-4">
        {!from && !read ? (
          <p className="text-sm text-muted-foreground">Checking which defaults change…</p>
        ) : read?.error ? (
          <p className="text-sm text-muted-foreground">
            Couldn't read version {instance.revision} to compare its defaults: {read.error}
          </p>
        ) : changes.length > 0 ? (
          <ul aria-label="Defaults that change" className="grid gap-1 text-sm">
            {changes.map((c) => (
              <li key={c.name}>
                <code className="font-mono">{c.name}</code>: {c.from || "(empty)"} → {c.to || "(empty)"}{" "}
                <span className="text-muted-foreground">(follows the compose file)</span>
              </li>
            ))}
          </ul>
        ) : null}
        {needs.length > 0 ? (
          <VariableFields
            vars={needs}
            values={values}
            onChange={(name, v) => setValues((prev) => ({ ...prev, [name]: v }))}
            showRequired={tried}
          />
        ) : (
          <p className="text-sm text-muted-foreground">No new values are needed.</p>
        )}
        {error ? <InlineError icon>{error}</InlineError> : null}
      </div>
    </ResponsiveDialog>
  );
}
