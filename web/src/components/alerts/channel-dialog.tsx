import { useState } from "react";
import { Send } from "lucide-react";
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
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { alertsPost } from "@/hooks/use-alerts";
import {
  buildChannel,
  channelService,
  channelSummary,
  SERVICES,
  serviceSpec,
  type Channel,
  type ServiceId,
} from "@/lib/alerts";
import { cn } from "@/lib/utils";

type Props = {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  // channel is the one being edited; undefined creates a new one.
  channel?: Channel;
  onSaved: () => void;
};

type TestResult = { ok: boolean; error: string };

// initialValues prefills a service form: stored non-secret fields when
// editing, field defaults otherwise. Secrets always start empty.
function initialValues(id: ServiceId, channel?: Channel): Record<string, string> {
  const values: Record<string, string> = {};
  for (const f of serviceSpec(id).fields) {
    values[f.key] = (!f.secret && channel?.fields?.[f.key]) || f.defaultValue || "";
  }
  return values;
}

// ChannelDialog creates or edits a notification channel. The service form
// builds the Shoutrrr URL in the browser; the URL is sent once and stored
// encrypted. Editing keeps the destination unless "Change destination" is
// used, which needs the secrets again.
export function ChannelDialog({ open, onOpenChange, channel, onSaved }: Props) {
  const editing = channel !== undefined;
  const [name, setName] = useState(channel?.name ?? "");
  const [service, setService] = useState<ServiceId>(channel ? channelService(channel) : "telegram");
  const [values, setValues] = useState<Record<string, string>>(() =>
    initialValues(channel ? channelService(channel) : "telegram", channel),
  );
  const [isDefault, setIsDefault] = useState(channel?.is_default ?? true);
  const [enabled, setEnabled] = useState(channel?.enabled ?? true);
  const [changing, setChanging] = useState(!editing);
  const [formError, setFormError] = useState<string | null>(null);
  const [busy, setBusy] = useState<"save" | "test" | null>(null);
  const [test, setTest] = useState<TestResult | null>(null);

  const spec = serviceSpec(service);

  const pickService = (id: ServiceId) => {
    setService(id);
    setValues(initialValues(id, channel && channelService(channel) === id ? channel : undefined));
    setFormError(null);
    setTest(null);
  };

  // destination builds the URL from the form, showing why it cannot.
  const destination = () => {
    try {
      const built = buildChannel(service, values);
      setFormError(null);
      return built;
    } catch (e) {
      setFormError(e instanceof Error ? e.message : "Invalid form");
      return null;
    }
  };

  const runTest = async () => {
    setTest(null);
    let body: Record<string, string>;
    if (changing) {
      const built = destination();
      if (!built) return;
      body = { url: built.url };
    } else {
      body = { id: channel!.id };
    }
    setBusy("test");
    try {
      setTest(await alertsPost<TestResult>("test-channel", body));
    } catch (e) {
      setTest({ ok: false, error: e instanceof Error ? e.message : "Test failed" });
    } finally {
      setBusy(null);
    }
  };

  const save = async () => {
    if (!name.trim()) {
      setFormError("Name is required");
      return;
    }
    let url = "";
    let fields: Record<string, string> = {};
    if (changing) {
      const built = destination();
      if (!built) return;
      ({ url, fields } = built);
    }
    setBusy("save");
    try {
      await alertsPost("save-channel", {
        id: channel?.id ?? "",
        name: name.trim(),
        url,
        fields,
        is_default: isDefault,
        enabled,
      });
      toast.success(editing ? "Channel updated" : "Channel added");
      onSaved();
      onOpenChange(false);
    } catch (e) {
      setFormError(e instanceof Error ? e.message : "Save failed");
    } finally {
      setBusy(null);
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{editing ? "Edit channel" : "Add channel"}</DialogTitle>
          <DialogDescription>
            Where alert notifications go. Secrets are encrypted on the server and never shown again.
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-4">
          <div className="space-y-1.5">
            <Label htmlFor="channel-name">Name</Label>
            <Input
              id="channel-name"
              value={name}
              maxLength={64}
              placeholder="Ops on Telegram"
              onChange={(e) => setName(e.target.value)}
            />
          </div>

          {changing ? (
            <>
              <div className="space-y-1.5">
                <Label>Service</Label>
                <div className="grid grid-cols-3 gap-1.5" role="radiogroup" aria-label="Service">
                  {SERVICES.map((s) => (
                    <button
                      key={s.id}
                      type="button"
                      role="radio"
                      aria-checked={service === s.id}
                      onClick={() => pickService(s.id)}
                      className={cn(
                        "rounded-md border px-2 py-1.5 text-sm transition-colors hover:border-primary/40",
                        service === s.id && "border-primary bg-primary/5 font-medium text-foreground",
                      )}
                    >
                      {s.label}
                    </button>
                  ))}
                </div>
              </div>
              {spec.fields.map((f) => (
                <div key={f.key} className="space-y-1.5">
                  <Label htmlFor={`channel-${f.key}`}>
                    {f.label}
                    {f.optional ? <span className="font-normal text-muted-foreground"> (optional)</span> : null}
                  </Label>
                  <Input
                    id={`channel-${f.key}`}
                    type={f.secret ? "password" : "text"}
                    autoComplete="off"
                    placeholder={f.placeholder}
                    value={values[f.key] ?? ""}
                    onChange={(e) => setValues((v) => ({ ...v, [f.key]: e.target.value }))}
                  />
                  {f.hint ? <p className="text-xs text-muted-foreground">{f.hint}</p> : null}
                </div>
              ))}
            </>
          ) : (
            <div className="flex items-center justify-between gap-3 rounded-md border px-3 py-2 text-sm">
              <span className="truncate" data-testid="channel-destination">
                {channelSummary(channel!)}
              </span>
              <Button variant="ghost" size="sm" onClick={() => setChanging(true)}>
                Change destination
              </Button>
            </div>
          )}

          <div className="flex items-center justify-between gap-3">
            <div>
              <Label htmlFor="channel-default">Default channel</Label>
              <p className="text-xs text-muted-foreground">Rules without their own channels notify here.</p>
            </div>
            <Switch id="channel-default" checked={isDefault} onCheckedChange={setIsDefault} />
          </div>
          <div className="flex items-center justify-between gap-3">
            <Label htmlFor="channel-enabled">Enabled</Label>
            <Switch id="channel-enabled" checked={enabled} onCheckedChange={setEnabled} />
          </div>

          {formError ? (
            <p role="alert" className="text-sm text-destructive">
              {formError}
            </p>
          ) : null}
          {test ? (
            <p role="status" className={cn("text-sm", test.ok ? "text-emerald-600 dark:text-emerald-400" : "text-destructive")}>
              {test.ok ? "Test notification sent." : `Test failed: ${test.error}`}
            </p>
          ) : null}
        </div>

        <DialogFooter className="gap-2 sm:justify-between">
          <Button variant="outline" onClick={() => void runTest()} disabled={busy !== null}>
            <Send className="size-4" /> {busy === "test" ? "Sending…" : "Send test"}
          </Button>
          <Button onClick={() => void save()} disabled={busy !== null}>
            {busy === "save" ? "Saving…" : "Save"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
