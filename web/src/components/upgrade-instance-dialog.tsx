import { useState } from "react";
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
import { fieldErrors, upgradeNeeds } from "@/lib/template-params";
import { sealSecrets, type InstanceView, type TemplateView } from "@/lib/templates";

type Props = {
  instance: InstanceView;
  // target is the revision to move to (its body says what it needs).
  target: TemplateView;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onDone: () => void;
};

// UpgradeInstanceDialog moves one instance to another revision of its
// template, asking only for the values the instance doesn't hold yet
// (a newly required parameter, a pending instance's secrets).
export function UpgradeInstanceDialog({ instance, target, open, onOpenChange, onDone }: Props) {
  const api = useTemplatesApi();
  const needs = upgradeNeeds(target.body.parameters, instance);
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
            Upgrade to r{target.revision}
          </DialogTitle>
          <DialogDescription>
            The app is redeployed with {target.name} r{target.revision}; its values and backup settings stay.
          </DialogDescription>
        </DialogHeader>
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
