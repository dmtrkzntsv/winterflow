import { useEffect, useState } from "react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { InlineError } from "@/components/inline-error";
import { ParameterFields } from "@/components/parameter-fields";
import { useTemplatesApi } from "@/hooks/use-templates-api";
import { AppStatusCode } from "@/context/apps-context-base";
import { defaultChanges, fieldErrors, upgradeNeeds } from "@/lib/template-params";
import { sealSecrets, type InstanceView, type TemplateView } from "@/lib/templates";

type Props = {
  instance: InstanceView;
  // appName is the app's name as the page shows it.
  appName: string;
  // target is the revision to move to (its body says what it needs).
  target: TemplateView;
  // current is the instance's own revision when the caller has it read;
  // otherwise the dialog reads it (to name the defaults that change).
  current?: TemplateView | null;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onDone: () => void;
};

// UpgradeInstanceDialog moves one instance to another revision of its
// template, asking only for the values the instance doesn't hold yet
// (a newly required parameter, a pending instance's secrets), and naming
// the parameters left on their default whose default the upgrade changes.
export function UpgradeInstanceDialog({ instance, appName, target, current, open, onOpenChange, onDone }: Props) {
  const api = useTemplatesApi();
  const needs = upgradeNeeds(target.body.parameters, instance);
  const given =
    current && current.id === instance.template_id && current.revision === instance.revision ? current : null;
  const [read, setRead] = useState<{ view: TemplateView | null; error: string } | null>(null);
  useEffect(() => {
    if (given) return;
    let live = true;
    api.getTemplate(instance.template_id, instance.revision).then(
      (view) => live && setRead({ view, error: "" }),
      (e: unknown) => live && setRead({ view: null, error: e instanceof Error ? e.message : "the read failed" }),
    );
    return () => {
      live = false;
    };
  }, [api, given, instance.template_id, instance.revision]);
  const from = given ?? read?.view ?? null;
  const changes = from ? defaultChanges(from.body.parameters, target.body.parameters, instance) : [];
  const starts = instance.state === "created" && instance.status_code === AppStatusCode.Stopped;
  const [values, setValues] = useState<Record<string, string>>({});
  const [tried, setTried] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const upgrade = async () => {
    setTried(true);
    if (Object.keys(fieldErrors(needs, values)).length > 0) return;
    setBusy(true);
    setError("");
    try {
      const plain: Record<string, string> = {};
      const typed: Record<string, string> = {};
      for (const p of needs) (p.secret ? typed : plain)[p.name] = values[p.name] ?? "";
      const secrets = await sealSecrets(api, instance.server_id, typed);
      await api.upgradeInstance({
        server_id: instance.server_id,
        key: instance.key,
        revision: target.revision,
        ...(Object.keys(plain).length ? { parameters: plain } : {}),
        ...(Object.keys(secrets).length ? { secrets } : {}),
      });
      toast.success(`Upgraded to r${target.revision}`);
      onOpenChange(false);
      onDone();
    } catch (e) {
      setError(e instanceof Error ? e.message : "The upgrade failed");
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={(o) => !busy && onOpenChange(o)}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            Upgrade {appName} to r{target.revision}
          </DialogTitle>
          <DialogDescription>
            The app is redeployed with {target.name} r{target.revision}; its backup settings stay. Values you set
            stay; parameters left on their default take the new default.{starts ? " The app will be started." : ""}
          </DialogDescription>
        </DialogHeader>
        {!from && !read ? (
          <p className="text-sm text-muted-foreground">Checking which defaults change…</p>
        ) : read?.error ? (
          <p className="text-sm text-muted-foreground">
            Couldn't read r{instance.revision} to compare its defaults: {read.error}
          </p>
        ) : changes.length > 0 ? (
          <ul aria-label="Defaults that change" className="grid gap-1 text-sm">
            {changes.map((c) => (
              <li key={c.name}>
                <code className="font-mono">{c.name}</code>: {c.from || "(empty)"} → {c.to || "(empty)"}{" "}
                <span className="text-muted-foreground">(follows the template default)</span>
              </li>
            ))}
          </ul>
        ) : null}
        {needs.length > 0 ? (
          <ParameterFields
            params={needs}
            values={values}
            onChange={(name, v) => setValues((prev) => ({ ...prev, [name]: v }))}
            showRequired={tried}
          />
        ) : (
          <p className="text-sm text-muted-foreground">No new values are needed.</p>
        )}
        {error ? <InlineError icon>{error}</InlineError> : null}
        <DialogFooter>
          <Button variant="outline" disabled={busy} onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button disabled={busy} onClick={() => void upgrade()}>
            {busy ? "Upgrading…" : `Upgrade to r${target.revision}`}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
