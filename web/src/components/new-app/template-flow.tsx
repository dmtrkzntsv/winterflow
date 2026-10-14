import { useEffect, useMemo, useRef, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { LayoutTemplate } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";
import { AppIcon } from "@/components/app-icon";
import { Disclosure } from "@/components/disclosure";
import { EmptyState } from "@/components/empty-state";
import { FormField } from "@/components/form-field";
import { HostHints } from "@/components/host-hints";
import { NativeSelect } from "@/components/native-select";
import { CreateBar } from "@/components/new-app/create-bar";
import { LoadError, PageSpinner } from "@/components/page-states";
import { ParameterFields } from "@/components/parameter-fields";
import { useApps } from "@/context/use-apps";
import { useBackups } from "@/context/use-backups";
import { useHostCheck } from "@/hooks/use-host-check";
import { useTemplateLooks } from "@/hooks/use-template-looks";
import { useTemplatesApi } from "@/hooks/use-templates-api";
import { validHostname } from "@/lib/domains";
import { errorHeadline } from "@/lib/errors";
import { focusFirstInvalid, invalidAttrs } from "@/lib/field-errors";
import { uniqueAppName } from "@/lib/new-app";
import { appPath, appsPath, serverSettingsPath, TEMPLATES_PATH } from "@/lib/routes";
import {
  expand,
  fieldErrors,
  newKey,
  plainValues,
  previewHosts,
  splitValues,
  type HostPreview,
} from "@/lib/template-params";
import {
  ApiError,
  sealSecrets,
  type BackupDefault,
  type TemplateSummary,
  type TemplateView,
  type UpdateInstanceBody,
} from "@/lib/templates";
import { cn } from "@/lib/utils";

// TemplateFlow is New app → From a template: pick a template (cards), fill
// in its required values (optional ones under "More options", defaults
// shown), pick storages for its backups, see the name and addresses the
// app will get, then Create app. Secret values are encrypted in the
// browser for this server. The version (latest by default) is under
// "Advanced".
export function TemplateFlow({ serverId }: { serverId: string }) {
  const navigate = useNavigate();
  const { apps, refresh, reconcile, noteDeployFailure, rename } = useApps();
  const { serverStorages } = useBackups();
  const api = useTemplatesApi();

  const [templates, setTemplates] = useState<TemplateSummary[] | null>(null);
  const looks = useTemplateLooks(api, templates);
  const [templateId, setTemplateId] = useState("");
  const [revision, setRevision] = useState(0); // 0: the latest
  const [view, setView] = useState<TemplateView | null>(null);
  const [values, setValues] = useState<Record<string, string>>({});
  // storages are this server's storage names (null while read).
  const [storages, setStorages] = useState<string[] | null>(null);
  // storageOverrides are the backup storages the user picked.
  const [storageOverrides, setStorageOverrides] = useState<Record<string, string>>({});
  const [loadError, setLoadError] = useState("");
  // attempt counts Retry clicks: each one runs the reads again.
  const [attempt, setAttempt] = useState(0);
  // key names this app to the API: a retry after a lost answer replays the
  // same create instead of adding a second app.
  const [key] = useState(newKey);
  // claimed: the API accepted the create (the key is taken), so a retry
  // after an agent failure is an update of that pending instance.
  const [claimed, setClaimed] = useState(false);
  const [tried, setTried] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [moreOpen, setMoreOpen] = useState(false);
  // name is the app name the user typed (null: the template's, made unique
  // on this server).
  const [name, setName] = useState<string | null>(null);
  const form = useRef<HTMLDivElement>(null);
  // sending is the synchronous guard against a second Create while one runs.
  const sending = useRef(false);

  useEffect(() => {
    let live = true;
    api.listTemplates().then(
      (list) => {
        if (!live) return;
        setTemplates(list);
        // One template: nothing to choose.
        if (list.length === 1) setTemplateId((id) => id || list[0].id);
      },
      (e: unknown) => live && setLoadError(e instanceof Error ? e.message : "Failed to load templates"),
    );
    return () => {
      live = false;
    };
  }, [api, attempt]);

  useEffect(() => {
    serverStorages().then(setStorages, () => setStorages([]));
  }, [serverStorages]);

  useEffect(() => {
    if (!templateId) return;
    let live = true;
    api.getTemplate(templateId, revision || undefined).then(
      (v) => {
        if (!live) return;
        setView(v);
        setStorageOverrides({});
      },
      (e: unknown) => live && setLoadError(e instanceof Error ? e.message : "Failed to load the template"),
    );
    return () => {
      live = false;
    };
  }, [api, templateId, revision, attempt]);

  const params = useMemo(() => view?.body.parameters ?? [], [view]);
  const required = params.filter((p) => p.required);
  const optional = params.filter((p) => !p.required);
  const errors = fieldErrors(params, values);
  const plain = useMemo(() => plainValues(params, values), [params, values]);
  const hosts = view ? previewHosts(view.body, plain) : [];
  const appName = view ? expand(view.body.app_name, plain) : "";
  const otherNames = apps.filter((a) => a.serverId === serverId).map((a) => a.name);
  const chosenName = name ?? uniqueAppName(appName, otherNames);
  const nameTaken =
    chosenName.trim() !== "" && otherNames.some((n) => n.trim().toLowerCase() === chosenName.trim().toLowerCase());
  // The name is asked for once the values are fine (it follows them).
  const nameError = tried && !chosenName.trim() && Object.keys(errors).length === 0 ? "Give the app a name." : undefined;
  const invalid = tried && (Object.keys(errors).length > 0 || nameError !== undefined);
  const backups = Object.entries(view?.body.backups ?? {});

  // storageFor is a backup's storage: the user's pick, else the template's
  // default, else (a backup without one) this server's first storage.
  const storageFor = (name: string, d: BackupDefault) =>
    storageOverrides[name] ?? (d.storage || storages?.[0] || "");

  // send creates the instance, or once the API holds this key (an earlier
  // attempt whose deploy failed: the instance is pending) updates it. A
  // refused create (a 4xx other than 409) claimed nothing; a 409 (the key is
  // held) or a 5xx (the claim came before the dispatch failed) may have.
  const send = async (body: UpdateInstanceBody, template: TemplateView) => {
    if (claimed) {
      try {
        await api.updateInstance(body);
        return;
      } catch (e) {
        if (!(e instanceof ApiError && e.status === 404)) throw e;
      }
    }
    setClaimed(true);
    try {
      await api.createInstance({ ...body, template_id: template.id, revision: template.revision });
    } catch (e) {
      if (e instanceof ApiError && e.status >= 400 && e.status < 500 && e.status !== 409) setClaimed(false);
      throw e;
    }
  };

  const create = async () => {
    if (!view || !serverId) return;
    setTried(true);
    const found = fieldErrors(params, values);
    if (Object.keys(found).length > 0 || !chosenName.trim()) {
      // Opened for good, so fixing the value doesn't fold it away under the cursor.
      if (optional.some((p) => found[p.name])) setMoreOpen(true);
      focusFirstInvalid(form.current);
      return;
    }
    if (sending.current) return;
    sending.current = true;
    setBusy(true);
    setError("");
    try {
      const { plain: given, typed } = splitValues(params, values);
      const parameters = Object.fromEntries(Object.entries(given).filter(([, v]) => v !== ""));
      const secrets = await sealSecrets(api, serverId, typed);
      const overrides: Record<string, string> = {};
      for (const [name, d] of backups) {
        const picked = storageFor(name, d);
        if (picked !== (d.storage ?? "")) overrides[name] = picked;
      }
      const body = {
        server_id: serverId,
        key,
        parameters,
        secrets,
        ...(Object.keys(overrides).length ? { backups: overrides } : {}),
      };
      try {
        await send(body, view);
      } catch (e) {
        // A deploy that failed may have left the app behind: open it.
        if (await openLeftApp(e)) return;
        throw e;
      }
      const [inst] = await Promise.all([api.getInstance(serverId, key), refresh()]);
      await nameApp(inst.app_id);
      toast.success("App created");
      navigate(appPath(serverId, inst.app_id));
    } catch (e) {
      setError(e instanceof Error ? e.message : "Couldn't create the app");
    } finally {
      sending.current = false;
      setBusy(false);
    }
  };

  // nameApp gives the new app the name the user chose when the template
  // renders another (the instance API takes no name: a rename does it). A
  // failed rename leaves the template's name and says so.
  const nameApp = async (appId: string) => {
    const want = chosenName.trim();
    if (!want || want === appName.trim()) return;
    try {
      await rename(appId, want);
    } catch (e) {
      toast.error(`Couldn't name the app ${want}`, { description: e instanceof Error ? e.message : undefined });
    }
  };

  // openLeftApp opens the app a failed create left behind (the API holds
  // the instance, pending, and the agent's list has its app), noting the
  // failure so the app reads Failed and says why. False when there is none.
  const openLeftApp = async (e: unknown): Promise<boolean> => {
    if (e instanceof ApiError) return false; // refused: nothing was dispatched
    const inst = await api.getInstance(serverId, key).catch(() => null);
    if (!inst?.app_id) return false;
    const list = await reconcile().catch(() => []);
    if (!list.some((a) => a.id === inst.app_id)) return false;
    const message = inst.last_error || (e instanceof Error ? e.message : "");
    if (message) noteDeployFailure(inst.app_id, message);
    await nameApp(inst.app_id);
    toast.error("The app was created but couldn't start", { description: message ? errorHeadline(message) : undefined });
    navigate(appPath(serverId, inst.app_id));
    return true;
  };

  if (loadError) {
    return (
      <LoadError
        message={loadError}
        onRetry={() => {
          setLoadError("");
          setAttempt((n) => n + 1);
        }}
      />
    );
  }
  if (templates === null) {
    // The cards' place is held, so the form doesn't jump when they arrive.
    return (
      <div role="status" aria-label="Loading templates" className="grid gap-3 sm:grid-cols-2">
        <Skeleton className="h-16 rounded-xl" />
        <Skeleton className="h-16 rounded-xl" />
      </div>
    );
  }
  if (templates.length === 0) {
    return (
      <EmptyState
        title="Your organization has no templates yet."
        icon={LayoutTemplate}
        action={
          <Button asChild variant="outline">
            <Link to={TEMPLATES_PATH}>Go to Templates</Link>
          </Button>
        }
      />
    );
  }

  const setValue = (name: string, v: string) => setValues((prev) => ({ ...prev, [name]: v }));

  return (
    <div ref={form} className="space-y-8">
      <div role="group" aria-label="Templates" className="grid gap-3 sm:grid-cols-2">
        {templates.map((t) => {
          const on = t.id === templateId;
          return (
            <button
              key={t.id}
              type="button"
              aria-pressed={on}
              disabled={claimed}
              onClick={() => {
                if (on) return;
                setTemplateId(t.id);
                setRevision(0);
                setView(null);
                setValues({});
                setName(null);
                // The next template's form starts untried and folded.
                setTried(false);
                setMoreOpen(false);
                setError("");
              }}
              className={cn(
                "flex min-h-16 items-center gap-3 rounded-xl border bg-card p-3 text-left transition-colors duration-150 hover:bg-muted/50 focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none disabled:cursor-not-allowed disabled:opacity-60 motion-reduce:transition-none",
                on && "border-primary ring-2 ring-primary/30",
              )}
            >
              <AppIcon
                name={t.name}
                icon={on && view ? view.body.icon : looks[t.id]?.icon}
                color={on && view ? view.body.color : looks[t.id]?.color}
                className="size-10 shrink-0 text-base"
              />
              <span className="min-w-0">
                <span className="block truncate font-medium">{t.name}</span>
                <span className="block text-xs text-muted-foreground">Version {t.latest_revision}</span>
              </span>
            </button>
          );
        })}
      </div>

      {!templateId ? (
        <p className="text-sm text-muted-foreground">Pick a template to continue.</p>
      ) : !view ? (
        <PageSpinner />
      ) : (
        <>
          <FormField
            id="instance-name"
            label="App name"
            error={nameError}
            hint={nameTaken ? "Another app on this server already has this name." : undefined}
            className="sm:max-w-sm"
          >
            <Input
              id="instance-name"
              value={chosenName}
              placeholder={appName.trim() || "App name"}
              {...invalidAttrs("instance-name", nameError)}
              onChange={(e) => setName(e.target.value)}
            />
          </FormField>

          <section aria-label="Values" className="space-y-4">
            {required.length > 0 ? (
              <ParameterFields params={required} values={values} onChange={setValue} showRequired={tried} />
            ) : optional.length === 0 ? (
              <p className="text-sm text-muted-foreground">This template needs no values.</p>
            ) : null}
            {optional.length > 0 ? (
              <Disclosure open={moreOpen} onOpenChange={setMoreOpen}>
                <p className="text-xs text-muted-foreground">Leave a field empty to use its default.</p>
                <ParameterFields params={optional} values={values} onChange={setValue} showRequired={tried} />
              </Disclosure>
            ) : null}
            <p className="text-xs text-muted-foreground">Secret values are encrypted in your browser for this server.</p>
          </section>

          {backups.length > 0 ? (
            <section aria-labelledby="backups-title" className="space-y-3">
              <h2 id="backups-title" className="text-[15px] font-semibold">
                Backups
              </h2>
              {backups.map(([name, d]) => {
                const value = storageFor(name, d);
                const missing = value !== "" && storages !== null && !storages.includes(value);
                return (
                  <div key={name} className="grid gap-1.5">
                    <Label htmlFor={`storage-${name}`}>
                      <span>
                        Keep backups of <code className="font-mono">{name}</code> in
                      </span>
                      {d.enabled ? null : <span className="text-xs font-normal text-muted-foreground">off by default</span>}
                    </Label>
                    <NativeSelect
                      id={`storage-${name}`}
                      aria-label={`Storage for ${name}`}
                      value={value}
                      onChange={(e) => setStorageOverrides((prev) => ({ ...prev, [name]: e.target.value }))}
                    >
                      {(d.storage ?? "") === "" ? <option value="">No storage</option> : null}
                      {missing ? <option value={value}>{value} (not on this server)</option> : null}
                      {(storages ?? []).map((s) => (
                        <option key={s} value={s}>
                          {s}
                        </option>
                      ))}
                    </NativeSelect>
                    {missing ? (
                      <p className="text-xs text-destructive">
                        This server has no storage named {value}.{" "}
                        <Link to={serverSettingsPath(serverId, "storages")} className="underline underline-offset-4">
                          Add it
                        </Link>{" "}
                        or pick another.
                      </p>
                    ) : null}
                  </div>
                );
              })}
            </section>
          ) : null}

          {hosts.length > 0 ? (
            <section aria-label="Summary" className="space-y-2 rounded-xl border bg-muted/30 p-4 text-sm">
              <ul aria-label="Addresses" className="space-y-2">
                {hosts.map((h, i) => (
                  <HostPreviewRow key={i} serverId={serverId} preview={h} />
                ))}
              </ul>
            </section>
          ) : null}

          <Disclosure label="Advanced">
            <div className="grid gap-1.5 sm:max-w-xs">
              <Label htmlFor="pick-version">Version</Label>
              <NativeSelect
                id="pick-version"
                value={String(revision)}
                disabled={claimed}
                onChange={(e) => setRevision(Number(e.target.value))}
              >
                <option value="0">Latest (version {view.latest_revision})</option>
                {(view.revisions ?? []).map((r) => (
                  <option key={r.revision} value={String(r.revision)}>
                    Version {r.revision}
                    {r.note ? ` · ${r.note}` : ""}
                  </option>
                ))}
              </NativeSelect>
            </div>
          </Disclosure>

              <CreateBar
            error={error}
            busy={busy}
            waiting={storages === null}
            onCreate={() => void create()}
            cancelTo={appsPath(serverId)}
            invalid={invalid}
          />
        </>
      )}
    </div>
  );
}

// HostPreviewRow is one address the app will get, with the live check
// (another app's host, or no registered domain covering it) once its
// values are filled in.
function HostPreviewRow({ serverId, preview }: { serverId: string; preview: HostPreview }) {
  const ready = preview.missing.length === 0 && validHostname(preview.host);
  const check = useHostCheck(serverId, undefined, preview.host, { enabled: ready && !!serverId, delay: 400 });
  return (
    <li>
      <span className="text-muted-foreground">Will be available at </span>
      <span className="font-mono">{preview.host || "…"}</span>
      {preview.missing.length > 0 ? (
        <p className="mt-1 text-xs text-muted-foreground">Fill in {preview.missing.join(", ")} to see the address.</p>
      ) : !validHostname(preview.host) ? (
        <p className="mt-1 text-xs text-destructive">This isn't a valid hostname yet.</p>
      ) : check ? (
        <HostHints id={`host-${preview.host}`} check={check} />
      ) : null}
    </li>
  );
}
