import { useEffect, useMemo, useRef, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { LayoutTemplate } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { AppIcon } from "@/components/app-icon";
import { Disclosure } from "@/components/disclosure";
import { EmptyState } from "@/components/empty-state";
import { FieldMessage, FormField } from "@/components/form-field";
import { HostnameField } from "@/components/hostname-field";
import { NativeSelect } from "@/components/native-select";
import { CreateBar } from "@/components/new-app/create-bar";
import { LoadError, PageSpinner } from "@/components/page-states";
import { VariableFields } from "@/components/variable-fields";
import { useApps } from "@/context/use-apps";
import { useBackups } from "@/context/use-backups";
import { useServers } from "@/context/use-servers";
import { useRegistrations } from "@/hooks/use-registrations";
import { useTemplateLooks } from "@/hooks/use-template-looks";
import { useTemplatesApi } from "@/hooks/use-templates-api";
import { validHostname } from "@/lib/domains";
import { errorHeadline } from "@/lib/errors";
import { focusFirstInvalid, invalidAttrs } from "@/lib/field-errors";
import { uniqueAppName } from "@/lib/new-app";
import { appPath, appsPath, serverSettingsPath, TEMPLATES_PATH } from "@/lib/routes";
import { newKey, splitValues, variableErrors } from "@/lib/template-values";
import {
  ApiError,
  orgTemplates,
  sealSecrets,
  type BackupChoice,
  type InstanceHost,
  type TemplateSummary,
  type TemplateView,
  type UpdateInstanceBody,
} from "@/lib/templates";
import { cn } from "@/lib/utils";

// TemplateFlow is New app → From a template: pick one of the org's
// templates (cards), name the app (the template's name by default), fill in
// the variables its compose file declares (required ones first, optional
// ones under "More options", the compose file's own values shown), give
// each http port it exposes a hostname when the server has registered
// domains, turn on the backups it declares with a storage each, then
// Create app. Secret values are encrypted in the browser for this server.
// The version (latest by default) is under "Advanced". initialTemplate is
// picked to begin with (a template page's Create app).
export function TemplateFlow({ serverId, initialTemplate = "" }: { serverId: string; initialTemplate?: string }) {
  const navigate = useNavigate();
  const { apps, refresh, reconcile, noteDeployFailure } = useApps();
  const { serverStorages } = useBackups();
  const { servers } = useServers();
  const ingressOn = Boolean(servers.find((s) => s.id === serverId)?.features?.ingress);
  const { registrations, refresh: refreshDomains } = useRegistrations(serverId, ingressOn);
  const api = useTemplatesApi();

  const [templates, setTemplates] = useState<TemplateSummary[] | null>(null);
  const looks = useTemplateLooks(api, templates);
  const [templateId, setTemplateId] = useState(initialTemplate);
  const [revision, setRevision] = useState(0); // 0: the latest
  const [view, setView] = useState<TemplateView | null>(null);
  const [values, setValues] = useState<Record<string, string>>({});
  // hosts are the hostnames typed per http expose target.
  const [hosts, setHosts] = useState<Record<string, string>>({});
  // storages are this server's storage names (null while read).
  const [storages, setStorages] = useState<string[] | null>(null);
  // backups are the backups turned on, with their storage, by name.
  const [backups, setBackups] = useState<Record<string, BackupChoice>>({});
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
      (all) => {
        if (!live) return;
        const list = orgTemplates(all);
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
      (v) => live && setView(v),
      (e: unknown) => live && setLoadError(e instanceof Error ? e.message : "Failed to load the template"),
    );
    return () => {
      live = false;
    };
  }, [api, templateId, revision, attempt]);

  const vars = useMemo(() => view?.detected.variables ?? [], [view]);
  const required = vars.filter((v) => v.required);
  const optional = vars.filter((v) => !v.required);
  const errors = variableErrors(vars, values);
  // Hostnames go to the http ports the compose file exposes, and only on a
  // server that routes them (a registered domain covers each).
  const targets = registrations.length > 0 ? (view?.detected.expose ?? []).filter((e) => e.protocol === "http") : [];
  const hostErrors: Record<string, string> = {};
  for (const t of targets) {
    const h = (hosts[t.target] ?? "").trim();
    if (h && !validHostname(h)) hostErrors[t.target] = "This isn't a valid hostname.";
  }
  const declaredBackups = view?.detected.backups ?? [];
  const backupErrors: Record<string, string> = {};
  for (const b of declaredBackups) {
    const c = backups[b.name];
    if (c?.enabled && !c.storage) backupErrors[b.name] = "Pick a storage to keep its backups in.";
  }
  const otherNames = apps.filter((a) => a.serverId === serverId).map((a) => a.name);
  const chosenName = name ?? uniqueAppName(view?.name ?? "", otherNames);
  const nameTaken =
    chosenName.trim() !== "" && otherNames.some((n) => n.trim().toLowerCase() === chosenName.trim().toLowerCase());
  const nameError = tried && !chosenName.trim() ? "Give the app a name." : undefined;
  const problems = Object.keys(errors).length + Object.keys(hostErrors).length + Object.keys(backupErrors).length;
  const invalid = tried && (problems > 0 || nameError !== undefined);

  // send creates the instance, or once the API holds this key (an earlier
  // attempt whose deploy failed: the instance is pending) updates it. A
  // refused create (a 4xx other than 409) claimed nothing; a 409 (the key is
  // held) or a 5xx (the claim came before the dispatch failed) may have.
  const send = async (body: UpdateInstanceBody, template: TemplateView, appName: string) => {
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
      await api.createInstance({ ...body, template_id: template.id, revision: template.revision, name: appName });
    } catch (e) {
      if (e instanceof ApiError && e.status >= 400 && e.status < 500 && e.status !== 409) setClaimed(false);
      throw e;
    }
  };

  const create = async () => {
    if (!view || !serverId) return;
    setTried(true);
    const found = variableErrors(vars, values);
    if (problems > 0 || !chosenName.trim()) {
      // Opened for good, so fixing the value doesn't fold it away under the cursor.
      if (optional.some((v) => found[v.name])) setMoreOpen(true);
      focusFirstInvalid(form.current);
      return;
    }
    if (sending.current) return;
    sending.current = true;
    setBusy(true);
    setError("");
    try {
      const { plain, typed } = splitValues(vars, values);
      const given = Object.fromEntries(Object.entries(plain).filter(([, v]) => v !== ""));
      const secrets = await sealSecrets(api, serverId, typed);
      const hostList: InstanceHost[] = targets
        .map((t) => ({ host: (hosts[t.target] ?? "").trim(), expose: t.target }))
        .filter((h) => h.host !== "");
      const on = Object.fromEntries(Object.entries(backups).filter(([, c]) => c.enabled));
      const body: UpdateInstanceBody = {
        server_id: serverId,
        key,
        ...(Object.keys(given).length ? { values: given } : {}),
        ...(Object.keys(secrets).length ? { secrets } : {}),
        ...(hostList.length ? { hosts: hostList } : {}),
        ...(Object.keys(on).length ? { backups: on } : {}),
      };
      try {
        await send(body, view, chosenName.trim());
      } catch (e) {
        // A deploy that failed may have left the app behind: open it.
        if (await openLeftApp(e)) return;
        throw e;
      }
      const [inst] = await Promise.all([api.getInstance(serverId, key), refresh()]);
      toast.success("App created");
      navigate(appPath(serverId, inst.app_id));
    } catch (e) {
      setError(e instanceof Error ? e.message : "Couldn't create the app");
    } finally {
      sending.current = false;
      setBusy(false);
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
  const setBackup = (name: string, patch: Partial<BackupChoice>) =>
    setBackups((prev) => {
      const cur = prev[name] ?? { enabled: false, storage: storages?.[0] ?? "" };
      return { ...prev, [name]: { ...cur, ...patch } };
    });

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
                setHosts({});
                setBackups({});
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
          {view.body.summary || view.body.description ? (
            <div className="space-y-1 text-sm">
              {view.body.summary ? <p className="font-medium">{view.body.summary}</p> : null}
              {view.body.description ? <p className="whitespace-pre-line text-muted-foreground">{view.body.description}</p> : null}
            </div>
          ) : null}

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
              placeholder={view.name}
              {...invalidAttrs("instance-name", nameError)}
              onChange={(e) => setName(e.target.value)}
            />
          </FormField>

          <section aria-label="Values" className="space-y-4">
            {required.length > 0 ? (
              <VariableFields vars={required} values={values} onChange={setValue} showRequired={tried} />
            ) : optional.length === 0 ? (
              <p className="text-sm text-muted-foreground">This template needs no values.</p>
            ) : null}
            {optional.length > 0 ? (
              <Disclosure open={moreOpen} onOpenChange={setMoreOpen}>
                <p className="text-xs text-muted-foreground">Leave a field empty to keep the compose file's value.</p>
                <VariableFields vars={optional} values={values} onChange={setValue} showRequired={tried} />
              </Disclosure>
            ) : null}
            <p className="text-xs text-muted-foreground">Secret values are encrypted in your browser for this server.</p>
          </section>

          {targets.length > 0 ? (
            <section aria-labelledby="hosts-title" className="space-y-3">
              <h2 id="hosts-title" className="text-[15px] font-semibold">
                Hostnames
              </h2>
              <p className="text-xs text-muted-foreground">Optional: a hostname for each web port the app exposes.</p>
              {targets.map((t) => (
                <HostnameField
                  key={t.id}
                  rowId={`host-${t.container}-${t.port}`}
                  value={hosts[t.target] ?? ""}
                  label={`Hostname for ${t.target}`}
                  placeholder="app.example.com"
                  rowClassName="flex flex-wrap items-center gap-2 sm:flex-nowrap"
                  inputClassName="basis-full sm:basis-auto"
                  checked
                  serverId={serverId}
                  registrations={registrations}
                  error={tried ? hostErrors[t.target] : undefined}
                  onChange={(v) => setHosts((prev) => ({ ...prev, [t.target]: v }))}
                  onRegistration={() => {}}
                  onReturn={refreshDomains}
                >
                  <span className="shrink-0 font-mono text-xs text-muted-foreground">→ {t.target}</span>
                </HostnameField>
              ))}
            </section>
          ) : null}

          {declaredBackups.length > 0 ? (
            <section aria-labelledby="backups-title" className="space-y-3">
              <h2 id="backups-title" className="text-[15px] font-semibold">
                Backups
              </h2>
              {declaredBackups.map((b) => {
                const c = backups[b.name] ?? { enabled: false, storage: storages?.[0] ?? "" };
                const id = `storage-${b.name}`;
                const message = tried ? backupErrors[b.name] : undefined;
                return (
                  <div key={b.name} className="grid gap-1.5">
                    <div className="flex flex-wrap items-center gap-3 max-sm:min-h-10">
                      <Switch
                        aria-label={`Back up ${b.name}`}
                        checked={c.enabled}
                        onCheckedChange={(enabled) => setBackup(b.name, { enabled })}
                      />
                      <Label htmlFor={id}>
                        Keep backups of <code className="font-mono">{b.name}</code> in
                      </Label>
                      <NativeSelect
                        id={id}
                        aria-label={`Storage for ${b.name}`}
                        value={c.storage ?? ""}
                        disabled={!c.enabled}
                        {...invalidAttrs(id, message, "max-sm:h-10")}
                        onChange={(e) => setBackup(b.name, { storage: e.target.value })}
                      >
                        {(storages ?? []).length === 0 ? <option value="">No storage</option> : null}
                        {(storages ?? []).map((s) => (
                          <option key={s} value={s}>
                            {s}
                          </option>
                        ))}
                      </NativeSelect>
                    </div>
                    <FieldMessage id={id} message={message} />
                    {storages !== null && storages.length === 0 ? (
                      <p className="text-xs text-muted-foreground">
                        This server has no storage yet.{" "}
                        <Link to={serverSettingsPath(serverId, "storages")} className="inline-flex items-center underline underline-offset-4 max-sm:min-h-10">
                          Add one
                        </Link>{" "}
                        to keep backups.
                      </p>
                    ) : null}
                  </div>
                );
              })}
            </section>
          ) : null}

          <Disclosure label="Advanced">
            <div className="grid gap-1.5 sm:max-w-xs">
              <Label htmlFor="pick-version">Version</Label>
              <NativeSelect
                id="pick-version"
                className="max-sm:h-10"
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
