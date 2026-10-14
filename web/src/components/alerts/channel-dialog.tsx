import { useState, type RefObject } from "react";
import { Send } from "lucide-react";
import { toast } from "sonner";

import { ServiceIcon } from "@/components/alerts/service-icon";
import { Disclosure } from "@/components/disclosure";
import { ResponsiveDialog } from "@/components/responsive-dialog";
import { StatusDot } from "@/components/status";
import { Button } from "@/components/ui/button";
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
  type FieldSpec,
  type ServiceId,
} from "@/lib/alerts";
import { returnFocusTo } from "@/lib/focus";
import { cn } from "@/lib/utils";

type Props = {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  // channel is the one being edited; undefined creates a new one.
  channel?: Channel;
  onSaved: () => void;
  // returnFocus is where focus goes back on close (the row it was opened
  // from may be gone).
  returnFocus?: RefObject<HTMLElement | null>;
};

type TestResult = { ok: boolean; error: string };

const FORM_ID = "channel-form";

// initialValues prefills a service form: stored non-secret fields when
// editing, field defaults otherwise. Secrets always start empty.
function initialValues(id: ServiceId, channel?: Channel): Record<string, string> {
  const values: Record<string, string> = {};
  for (const f of serviceSpec(id).fields) {
    values[f.key] = (!f.secret && channel?.fields?.[f.key]) || f.defaultValue || "";
  }
  return values;
}

// advancedShown says whether a service form opens with Advanced unfolded:
// one of its advanced fields holds something other than its default.
function advancedShown(id: ServiceId, values: Record<string, string>): boolean {
  return serviceSpec(id).fields.some((f) => f.advanced && (values[f.key] ?? "") !== (f.defaultValue ?? ""));
}

// ChannelDialog creates or edits a notification channel. The service form
// builds the Shoutrrr URL in the browser; the URL is sent once and stored
// encrypted. Editing keeps the destination (and so its secrets) unless
// "Change destination" is used, which needs the secrets again. Optional
// connection details (SMTP port and sign-in) sit under "Advanced", which
// opens by itself when the form can't be built. Its parent mounts it per
// opening, so typed secrets do not outlive it.
export function ChannelDialog({ open, onOpenChange, channel, onSaved, returnFocus }: Props) {
  const editing = channel !== undefined;
  const firstService: ServiceId = channel ? channelService(channel) : "telegram";
  const [name, setName] = useState(channel?.name ?? "");
  const [service, setService] = useState<ServiceId>(firstService);
  const [values, setValues] = useState<Record<string, string>>(() => initialValues(firstService, channel));
  const [advanced, setAdvanced] = useState(() => advancedShown(firstService, initialValues(firstService, channel)));
  const [isDefault, setIsDefault] = useState(channel?.is_default ?? true);
  const [enabled, setEnabled] = useState(channel?.enabled ?? true);
  const [changing, setChanging] = useState(!editing);
  const [formError, setFormError] = useState<string | null>(null);
  const [busy, setBusy] = useState<"save" | "test" | null>(null);
  const [test, setTest] = useState<TestResult | null>(null);

  const spec = serviceSpec(service);
  const basic = spec.fields.filter((f) => !f.advanced);
  const more = spec.fields.filter((f) => f.advanced);

  const pickService = (id: ServiceId) => {
    const next = initialValues(id, channel && channelService(channel) === id ? channel : undefined);
    setService(id);
    setValues(next);
    setAdvanced(advancedShown(id, next));
    setFormError(null);
    setTest(null);
  };

  // destination builds the URL from the form, showing why it cannot (and
  // the folded fields, where the problem may be).
  const destination = () => {
    try {
      const built = buildChannel(service, values);
      setFormError(null);
      return built;
    } catch (e) {
      setFormError(e instanceof Error ? e.message : "Invalid form");
      if (more.length > 0) setAdvanced(true);
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

  const input = (f: FieldSpec) => (
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
  );

  return (
    <ResponsiveDialog
      open={open}
      onOpenChange={onOpenChange}
      title={editing ? "Edit channel" : "Add channel"}
      description="Where alert notifications go. Secrets are encrypted on the server and never shown again."
      className="sm:max-w-lg"
      onCloseAutoFocus={returnFocusTo(returnFocus)}
      footer={
        <>
          <Button type="button" variant="outline" className="max-sm:h-10" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button
            type="button"
            variant="outline"
            className="max-sm:h-10"
            onClick={() => void runTest()}
            disabled={busy !== null}
          >
            <Send className="size-4" /> {busy === "test" ? "Sending…" : "Send test"}
          </Button>
          <Button type="submit" form={FORM_ID} className="max-sm:h-10" disabled={busy !== null}>
            {busy === "save" ? "Saving…" : "Save"}
          </Button>
        </>
      }
    >
      <form
        id={FORM_ID}
        className="space-y-4"
        onSubmit={(e) => {
          e.preventDefault();
          void save();
        }}
      >
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
              <Label id="channel-service-label">Service</Label>
              <div className="grid grid-cols-3 gap-2" role="radiogroup" aria-labelledby="channel-service-label">
                {SERVICES.map((s) => (
                  <button
                    key={s.id}
                    type="button"
                    role="radio"
                    aria-checked={service === s.id}
                    onClick={() => pickService(s.id)}
                    className={cn(
                      "flex min-h-16 flex-col items-center justify-center gap-1 rounded-lg border px-2 py-2 text-xs transition-colors duration-150 hover:border-primary/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
                      service === s.id && "border-primary bg-primary/5 font-medium text-foreground",
                    )}
                  >
                    <ServiceIcon service={s.id} className="size-5" />
                    {s.label}
                  </button>
                ))}
              </div>
            </div>
            {basic.map(input)}
            {more.length > 0 ? (
              <Disclosure label="Advanced" open={advanced} onOpenChange={setAdvanced}>
                {more.map(input)}
              </Disclosure>
            ) : null}
          </>
        ) : (
          <div className="flex items-center justify-between gap-3 rounded-md border px-3 py-2 text-sm">
            <span className="truncate" data-testid="channel-destination">
              {channelSummary(channel!)}
            </span>
            <Button type="button" variant="ghost" size="sm" className="max-sm:h-10" onClick={() => setChanging(true)}>
              Change destination
            </Button>
          </div>
        )}

        <div className="flex items-center justify-between gap-3">
          <div>
            <Label htmlFor="channel-default" className="max-sm:py-1.5">
              Default channel
            </Label>
            <p className="text-xs text-muted-foreground">Rules without their own channels notify here.</p>
          </div>
          <Switch id="channel-default" checked={isDefault} onCheckedChange={setIsDefault} />
        </div>
        <div className="flex items-center justify-between gap-3">
          <Label htmlFor="channel-enabled" className="max-sm:py-2.5">
            Enabled
          </Label>
          <Switch id="channel-enabled" checked={enabled} onCheckedChange={setEnabled} />
        </div>

        {formError ? (
          <p role="alert" className="text-sm text-destructive">
            {formError}
          </p>
        ) : null}
        {test ? (
          <p role="status" className={cn("flex items-center gap-2 text-sm", !test.ok && "text-destructive")}>
            {test.ok ? (
              <>
                <StatusDot tone="ok" /> Test notification sent.
              </>
            ) : (
              `Test failed: ${test.error}`
            )}
          </p>
        ) : null}
      </form>
    </ResponsiveDialog>
  );
}
